package userservice

import (
	"bytes"
	"encoding/xml"
	"strings"
)

func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func quoteUnit(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`).Replace(s) + `"`
}
func (s Spec) args() []string {
	return []string{"--data-dir", s.Root, "service-run", "--kind", string(s.Kind), "--id", string(s.ID)}
}
func quoteWindows(s string) string {
	// CommandLineToArgvW / Go argv escaping; no shell or PowerShell source is
	// constructed from executable/scope paths.
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range s {
		if r == '\\' {
			slashes++
			continue
		}
		if r == '"' {
			b.WriteString(strings.Repeat("\\", slashes*2+1))
			b.WriteRune(r)
		} else {
			b.WriteString(strings.Repeat("\\", slashes))
			b.WriteRune(r)
		}
		slashes = 0
	}
	b.WriteString(strings.Repeat("\\", slashes*2))
	b.WriteByte('"')
	return b.String()
}
func definition(s Spec) []byte {
	args := s.args()
	switch s.Platform {
	case "darwin":
		a := "<string>" + xmlText(s.Binary) + "</string>"
		for _, v := range args {
			a += "<string>" + xmlText(v) + "</string>"
		}
		return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Label</key><string>` + s.Name + `</string><key>ProgramArguments</key><array>` + a + `</array><key>RunAtLoad</key><true/><key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict><key>ProcessType</key><string>Background</string><key>Umask</key><integer>63</integer><key>AbandonProcessGroup</key><true/></dict></plist>
`)
	case "linux":
		a := quoteUnit(s.Binary)
		for _, v := range args {
			a += " " + quoteUnit(v)
		}
		return []byte("[Unit]\nDescription=DeliDev current-user " + string(s.Kind) + "\n\n[Service]\nType=exec\nExecStart=" + a + "\nRestart=on-failure\nRestartSec=5\nUMask=0077\nKillMode=process\nTimeoutStopSec=30\n\n[Install]\nWantedBy=default.target\n")
	case "windows":
		a := []string{}
		for _, v := range args {
			a = append(a, quoteWindows(v))
		}
		return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><RegistrationInfo><URI>` + xmlText(s.DefinitionPath) + `</URI></RegistrationInfo><Triggers><LogonTrigger><Enabled>true</Enabled><UserId>` + s.User + `</UserId></LogonTrigger></Triggers><Principals><Principal id="Owner"><UserId>` + s.User + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals><Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><AllowHardTerminate>false</AllowHardTerminate><StartWhenAvailable>false</StartWhenAvailable><RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable><IdleSettings><StopOnIdleEnd>false</StopOnIdleEnd><RestartOnIdle>false</RestartOnIdle></IdleSettings><AllowStartOnDemand>true</AllowStartOnDemand><Enabled>false</Enabled><Hidden>false</Hidden><RunOnlyIfIdle>false</RunOnlyIfIdle><WakeToRun>false</WakeToRun><ExecutionTimeLimit>PT0S</ExecutionTimeLimit><Priority>7</Priority></Settings><Actions Context="Owner"><Exec><Command>` + xmlText(s.Binary) + `</Command><Arguments>` + xmlText(strings.Join(a, " ")) + `</Arguments></Exec></Actions></Task>`)
	}
	return nil
}
