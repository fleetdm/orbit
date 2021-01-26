package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/fleetdm/orbit/pkg/constant"
	"github.com/fleetdm/orbit/pkg/database"
	"github.com/fleetdm/orbit/pkg/insecure"
	"github.com/fleetdm/orbit/pkg/osquery"
	"github.com/fleetdm/orbit/pkg/update"
	"github.com/fleetdm/orbit/pkg/update/filestore"
	"github.com/oklog/run"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
)

const (
	tufURL         = "https://tuf.fleetctl.com"
	certPath       = "/tmp/fleet.pem"
	defaultRootDir = "/var/lib/fleet/orbit"
)

func main() {
	log.Logger = log.Output(
		zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339Nano},
	)
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	app := cli.NewApp()
	app.Name = "Orbit osquery"
	app.Usage = "A powered-up, (near) drop-in replacement for osquery"
	app.Commands = []*cli.Command{
		shellCommand,
	}
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "root-dir",
			Usage:   "Root directory for Orbit state",
			Value:   defaultRootDir,
			EnvVars: []string{"ORBIT_ROOT_DIR"},
		},
		&cli.BoolFlag{
			Name:    "insecure",
			Usage:   "Disable TLS certificate verification",
			EnvVars: []string{"ORBIT_INSECURE"},
		},
		&cli.StringFlag{
			Name:    "fleet-url",
			Usage:   "URL (host:port) of Fleet server",
			EnvVars: []string{"ORBIT_FLEET_URL"},
		},
		&cli.StringFlag{
			Name:    "tuf-url",
			Usage:   "URL of TUF update server",
			Value:   tufURL,
			EnvVars: []string{"ORBIT_TUF_URL"},
		},
		&cli.StringFlag{
			Name:    "enroll-secret",
			Usage:   "Enroll secret for authenticating to Fleet server",
			EnvVars: []string{"ORBIT_ENROLL_SECRET"},
		},
		&cli.StringFlag{
			Name:    "osquery-version",
			Usage:   "Version of osquery to use",
			Value:   "stable",
			EnvVars: []string{"ORBIT_OSQUERY_VERSION"},
		},
		&cli.BoolFlag{
			Name:    "debug",
			Usage:   "Enable debug logging",
			EnvVars: []string{"ORBIT_DEBUG"},
		},
	}
	app.Action = func(c *cli.Context) error {
		if c.Bool("debug") {
			zerolog.SetGlobalLevel(zerolog.DebugLevel)
		}

		if err := os.MkdirAll(c.String("root-dir"), constant.DefaultDirMode); err != nil {
			return errors.Wrap(err, "initialize root dir")
		}

		db, err := database.Open(filepath.Join(c.String("root-dir"), "orbit.db"))
		if err != nil {
			return err
		}
		defer func() {
			if err := db.Close(); err != nil {
				log.Error().Err(err).Msg("Close badger")
			}
		}()

		localStore, err := filestore.New(filepath.Join(c.String("root-dir"), "tuf-metadata.json"))
		if err != nil {
			log.Fatal().Err(err).Msg("failed to create local metadata store")
		}

		// Initialize updater and get expected version
		opt := update.DefaultOptions
		opt.RootDirectory = c.String("root-dir")
		opt.ServerURL = c.String("tuf-url")
		opt.LocalStore = localStore
		opt.InsecureTransport = c.Bool("insecure")
		updater, err := update.New(opt)
		if err != nil {
			return err
		}
		if err := updater.UpdateMetadata(); err != nil {
			log.Info().Err(err).Msg("failed to update metadata. using saved metadata.")
		}
		osquerydPath, err := updater.Get("osqueryd", c.String("osquery-version"))
		if err != nil {
			return err
		}

		var g run.Group
		var options []func(*osquery.Runner) error
		options = append(options, osquery.WithDataPath(c.String("root-dir")))

		fleetURL := c.String("fleet-url")

		if c.Bool("insecure") {
			proxy, err := insecure.NewTLSProxy(fleetURL)
			if err != nil {
				return errors.Wrap(err, "create TLS proxy")
			}

			g.Add(
				func() error {
					log.Info().
						Str("addr", fmt.Sprintf("localhost:%d", proxy.Port)).
						Str("target", c.String("fleet-url")).
						Msg("using insecure TLS proxy")
					err := proxy.InsecureServeTLS()
					return err
				},
				func(error) {
					if err := proxy.Close(); err != nil {
						log.Error().Err(err).Msg("close proxy")
					}
				},
			)

			// Write cert that proxy uses
			err = ioutil.WriteFile(certPath, []byte(insecure.ServerCert), os.ModePerm)
			if err != nil {
				return errors.Wrap(err, "write server cert")
			}

			// Rewrite URL to the proxy URL
			fleetURL = fmt.Sprintf("localhost:%d", proxy.Port)

			options = append(options,
				osquery.WithFlags(osquery.FleetFlags(fleetURL)),
				osquery.WithFlags([]string{"--tls_server_certs", certPath}),
			)
		}

		if enrollSecret := c.String("enroll-secret"); enrollSecret != "" {
			options = append(options,
				osquery.WithEnv([]string{"ENROLL_SECRET=" + enrollSecret}),
				osquery.WithFlags([]string{"--enroll_secret_env", "ENROLL_SECRET"}),
			)
		}

		if fleetURL != "" {
			options = append(options,
				osquery.WithFlags(osquery.FleetFlags(fleetURL)),
			)
		}

		if c.Bool("debug") {
			options = append(options,
				osquery.WithFlags([]string{"--verbose", "--tls_dump"}),
			)
		}

		options = append(options, osquery.WithFlags([]string(c.Args().Slice())))

		// Create an osquery runner with the provided options
		r, _ := osquery.NewRunner(osquerydPath, options...)
		g.Add(r.Execute, r.Interrupt)

		// Install a signal handler
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		g.Add(run.SignalHandler(ctx, os.Interrupt, os.Kill))

		if err := g.Run(); err != nil {
			log.Error().Err(err).Msg("unexpected exit")
		}

		return nil
	}

	if err := app.Run(os.Args); err != nil {
		log.Error().Err(err).Msg("")
	}
}

var shellCommand = &cli.Command{
	Name:    "shell",
	Aliases: []string{"osqueryi"},
	Usage:   "Run the osqueryi shell",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "osquery-version",
			Usage:   "Version of osquery to use",
			Value:   "stable",
			EnvVars: []string{"ORBIT_OSQUERY_VERSION"},
		},
		&cli.BoolFlag{
			Name:    "debug",
			Usage:   "Enable debug logging",
			EnvVars: []string{"ORBIT_DEBUG"},
		},
	},
	Action: func(c *cli.Context) error {
		if c.Bool("debug") {
			zerolog.SetGlobalLevel(zerolog.DebugLevel)
		}

		if err := os.MkdirAll(c.String("root-dir"), constant.DefaultDirMode); err != nil {
			return errors.Wrap(err, "initialize root dir")
		}

		db, err := database.Open(filepath.Join(c.String("root-dir"), "orbit.db"))
		if err != nil {
			return err
		}
		defer func() {
			if err := db.Close(); err != nil {
				log.Error().Err(err).Msg("close badger")
			}
		}()

		localStore, err := filestore.New(filepath.Join(c.String("root-dir"), "tuf-metadata.json"))
		if err != nil {
			log.Fatal().Err(err).Msg("failed to create local metadata store")
		}

		// Initialize updater and get expected version
		opt := update.DefaultOptions
		opt.RootDirectory = c.String("root-dir")
		opt.ServerURL = c.String("tuf-url")
		opt.LocalStore = localStore
		opt.InsecureTransport = c.Bool("insecure")
		updater, err := update.New(opt)
		if err != nil {
			return err
		}
		if err := updater.UpdateMetadata(); err != nil {
			log.Info().Err(err).Msg("failed to update metadata. using saved metadata.")
		}
		osquerydPath, err := updater.Get("osqueryd", c.String("osquery-version"))
		if err != nil {
			return err
		}

		var g run.Group

		// Create an osquery runner with the provided options
		r, _ := osquery.NewRunner(
			osquerydPath,
			osquery.WithShell(),
			osquery.WithFlags([]string(c.Args().Slice())),
			osquery.WithDataPath(c.String("root-dir")),
		)
		g.Add(r.Execute, r.Interrupt)

		// Install a signal handler
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		g.Add(run.SignalHandler(ctx, os.Interrupt, os.Kill))

		if err := g.Run(); err != nil {
			log.Error().Err(err).Msg("unexpected exit")
		}

		return nil
	},
}