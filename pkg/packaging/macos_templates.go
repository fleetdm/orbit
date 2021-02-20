package packaging

import "text/template"

// Best reference I could find:
// http://s.sudre.free.fr/Stuff/Ivanhoe/FLAT.html
var macosPackageInfoTemplate = template.Must(template.New("").Option("missingkey=error").Parse(
	`<pkg-info format-version="2" identifier="{{.Identifier}}.base.pkg" version="{{.Version}}" install-location="/" auth="root">
  <scripts>
    <postinstall file="./postinstall"/>
  </scripts>
  <bundle-version>
  </bundle-version>
</pkg-info>
`))

// Reference:
// https://developer.apple.com/library/archive/documentation/DeveloperTools/Reference/DistributionDefinitionRef/Chapters/Distribution_XML_Ref.html
var macosDistributionTemplate = template.Must(template.New("").Option("missingkey=error").Parse(
	`<?xml version="1.0" encoding="utf-8"?>
<installer-gui-script minSpecVersion="2">
	<title>Orbit</title>
	<choices-outline>
	    <line choice="choiceBase"/>
    </choices-outline>
    <choice id="choiceBase" title="base">
        <pkg-ref id="{{.Identifier}}.base.pkg"/>
    </choice>
    <pkg-ref id="{{.Identifier}}.base.pkg" version="{{.Version}}" auth="root">#base.pkg</pkg-ref>
</installer-gui-script>
`))

var macosPostinstallTemplate = template.Must(template.New("").Option("missingkey=error").Parse(
	`#!/bin/bash

ln -sf /var/lib/orbit/orbit /usr/local/bin/orbit

{{ if .StartService -}}
launchctl stop com.fleetdm.orbit

sleep 3

launchctl unload /Library/LaunchDaemons/com.fleetdm.orbit.plist
launchctl load /Library/LaunchDaemons/com.fleetdm.orbit.plist
{{- end }}
`))

// TODO set Nice?
var macosLaunchdTemplate = template.Must(template.New("").Option("missingkey=error").Parse(
	`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key>
    <string>com.fleetdm.orbit</string>
    <key>ProgramArguments</key>
    <array>
       <string>/var/lib/orbit/orbit</string>
    </array>
    <key>StandardOutPath</key>
    <string>/var/log/orbit/orbit.stdout.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/orbit/orbit.stderr.log</string>
    <key>EnvironmentVariables</key>
    <dict>
      {{ if .Insecure }}<key>ORBIT_INSECURE</key><string>true</string>{{ end }}
      {{ if .FleetURL }}<key>ORBIT_FLEET_URL</key><string>{{.FleetURL}}</string>{{ end }}
      {{ if .EnrollSecret }}<key>ORBIT_ENROLL_SECRET_PATH</key><string>/var/lib/orbit/secret</string>{{ end }}
    </dict>
    <key>KeepAlive</key><true/>
    <key>RunAtLoad</key><true/>
    <key>ThrottleInterval</key>
    <integer>10</integer>
  </dict>
</plist>
`))