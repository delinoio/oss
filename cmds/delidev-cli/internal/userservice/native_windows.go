package userservice

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"golang.org/x/sys/windows"
)

// The script is constant. All dynamic values arrive as bounded JSON on stdin,
// never PowerShell source, shell interpolation, credential argv or passwords.
const schedulerScript = `[Console]::InputEncoding=[Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)
$ErrorActionPreference='Stop'
try {
 $x=([Console]::In.ReadToEnd() | ConvertFrom-Json)
 $sid=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value
 if($sid -ne $x.user -or [Diagnostics.Process]::GetCurrentProcess().SessionId -eq 0){exit 12}
 $s=New-Object -ComObject 'Schedule.Service'; $s.Connect(); $f=$s.GetFolder('\')
 $t=$null
 try {$t=$f.GetTask($x.name)} catch {if($_.Exception.HResult -ne -2147024894){throw}}
 switch($x.action){
 'query' { if($null -eq $t){@{present=$false} | ConvertTo-Json -Compress; exit 0}
   $engines=@(); foreach($v in $t.GetInstances(0)){$engines+=@{pid=[int]$v.EnginePID; id=[string]$v.InstanceGuid}}
   $parent=0
   if($x.pid -gt 0){$p=Get-CimInstance Win32_Process -Filter ('ProcessId = '+[int]$x.pid); if($null -ne $p){$parent=[int]$p.ParentProcessId}}
   @{present=$true; enabled=[bool]$t.Enabled; xml=[string]$t.Xml; engines=$engines; parent=$parent} | ConvertTo-Json -Depth 4 -Compress
 }
 'install' { if($null -ne $t){exit 13}; $t=$f.RegisterTask($x.name,$x.xml,2,$sid,$null,3,$null); @{xml=[string]$t.Xml} | ConvertTo-Json -Compress }
 'enable' {if($null -eq $t){exit 13}; $t.Enabled=$true}
 'start' {if($null -eq $t){exit 13}; $null=$t.Run($null)}
 'disable' {if($null -eq $t){exit 13}; $t.Enabled=$false}
 'remove' {if($null -eq $t -or $t.GetInstances(0).Count -ne 0){exit 13}; $f.DeleteTask($x.name,0)}
 default {exit 13}
 }
} catch {exit 12}
`

type taskReply struct {
	Present bool   `json:"present"`
	Enabled bool   `json:"enabled"`
	XML     string `json:"xml"`
	Engines []struct {
		PID int    `json:"pid"`
		ID  string `json:"id"`
	} `json:"engines"`
	Parent int `json:"parent"`
}

func taskCall(ctx context.Context, s Spec, action string, pid int) (taskReply, error) {
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		return taskReply{}, unavailable()
	}
	input, _ := json.Marshal(map[string]any{"name": s.DefinitionPath, "user": s.User, "action": action, "xml": string(definition(s)), "pid": pid})
	out, err := nativeCommand(ctx, filepath.Join(sys, "WindowsPowerShell", "v1.0", "powershell.exe"), []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", schedulerScript}, nativeEnv("windows"), input)
	if err != nil {
		return taskReply{}, unavailable()
	}
	var r taskReply
	if len(out) > 0 && json.Unmarshal(out, &r) != nil {
		return r, failure()
	}
	return r, nil
}
func taskCanonical(raw []byte) ([]byte, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var b bytes.Buffer
	enc := xml.NewEncoder(&b)
	stack := []string{}
	for {
		t, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, failure()
		}
		switch v := t.(type) {
		case xml.StartElement:
			stack = append(stack, v.Name.Local)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, failure()
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if strings.TrimSpace(string(v)) == "" {
				continue
			}
			if len(stack) == 3 && stack[1] == "Settings" && stack[2] == "Enabled" {
				t = xml.CharData("CONTROLLED")
			}
		case xml.ProcInst, xml.Comment:
			continue
		case xml.Directive:
			return nil, failure()
		}
		if enc.EncodeToken(t) != nil {
			return nil, failure()
		}
	}
	if enc.Flush() != nil || len(stack) != 0 {
		return nil, failure()
	}
	return b.Bytes(), nil
}
func validateTask(s Spec, raw []byte) error {
	var task struct {
		Principals struct {
			Items []struct {
				UserID   string `xml:"UserId"`
				Logon    string `xml:"LogonType"`
				RunLevel string `xml:"RunLevel"`
			} `xml:"Principal"`
		} `xml:"Principals"`
		Triggers struct {
			Items []struct {
				UserID string `xml:"UserId"`
			} `xml:"LogonTrigger"`
		} `xml:"Triggers"`
		Actions struct {
			Items []struct {
				Command   string
				Arguments string
			} `xml:"Exec"`
		} `xml:"Actions"`
		Settings struct {
			Instances string `xml:"MultipleInstancesPolicy"`
			Terminate string `xml:"AllowHardTerminate"`
			Limit     string `xml:"ExecutionTimeLimit"`
		} `xml:"Settings"`
	}
	if xml.Unmarshal(raw, &task) != nil || len(task.Principals.Items) != 1 || len(task.Triggers.Items) != 1 || len(task.Actions.Items) != 1 {
		return failure()
	}
	p := task.Principals.Items[0]
	a := task.Actions.Items[0]
	args := []string{}
	for _, v := range s.args() {
		args = append(args, quoteWindows(v))
	}
	if p.UserID != s.User || p.Logon != "InteractiveToken" || p.RunLevel != "LeastPrivilege" || task.Triggers.Items[0].UserID != s.User || a.Command != s.Binary || a.Arguments != strings.Join(args, " ") || task.Settings.Instances != "IgnoreNew" || task.Settings.Terminate != "false" || task.Settings.Limit != "PT0S" {
		return failure()
	}
	return nil
}
func taskBaseline(s Spec) string {
	return filepath.Join(s.Root, "user-service-"+string(s.Kind)+"-task-"+string(s.ID)+".xml")
}
func (nativeBackend) Inspect(ctx context.Context, s Spec) (Observation, error) {
	m := New(s.Root, s.Kind, nil)
	r, err := m.runtime(s.ID)
	if err != nil {
		return Observation{}, err
	}
	response, err := taskCall(ctx, s, "query", r.PID)
	if err != nil {
		return Observation{}, err
	}
	if !response.Present {
		return Observation{}, nil
	}
	baseline, err := security.ReadPrivate(taskBaseline(s), 64<<10)
	if err != nil {
		return Observation{}, failure()
	}
	if validateTask(s, []byte(response.XML)) != nil {
		return Observation{}, failure()
	}
	expected, e := taskCanonical(baseline)
	actual, e2 := taskCanonical([]byte(response.XML))
	if e != nil || e2 != nil || !bytes.Equal(expected, actual) {
		return Observation{}, failure()
	}
	pid := 0
	if len(response.Engines) > 1 {
		return Observation{}, failure()
	}
	if len(response.Engines) == 1 {
		// EnginePID names the task engine, not necessarily its action. Bind it to
		// the original action's kernel parent, then Manager validates birth/user/exe.
		if r.PID <= 0 || response.Engines[0].PID <= 0 || response.Engines[0].PID != response.Parent {
			return Observation{}, failure()
		}
		pid = r.PID
	}
	return Observation{Present: true, Enabled: response.Enabled, PID: pid}, nil
}
func (nativeBackend) Install(ctx context.Context, s Spec) error {
	response, err := taskCall(ctx, s, "install", 0)
	if err != nil {
		return err
	}
	if validateTask(s, []byte(response.XML)) != nil {
		return failure()
	}
	// Retain the native canonical expansion from the exclusive CREATE operation;
	// future reads must match its complete graph, apart from controlled Enabled.
	path := taskBaseline(s)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return failure()
	}
	_, err = f.WriteString(response.XML)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil || ce != nil {
		return failure()
	}
	return security.SyncParent(path)
}
func (nativeBackend) Enable(ctx context.Context, s Spec) error {
	_, e := taskCall(ctx, s, "enable", 0)
	return e
}
func (nativeBackend) Start(ctx context.Context, s Spec) error {
	_, e := taskCall(ctx, s, "start", 0)
	return e
}
func (nativeBackend) Disable(ctx context.Context, s Spec) error {
	_, e := taskCall(ctx, s, "disable", 0)
	return e
}
func (nativeBackend) Remove(ctx context.Context, s Spec) error {
	_, e := taskCall(ctx, s, "remove", 0)
	return e
}
