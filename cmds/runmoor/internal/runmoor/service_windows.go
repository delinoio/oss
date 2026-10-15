package runmoor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/semver"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type windowsTaskManifest struct {
	Schema                   int
	SID, Binary, Config, XML string
}
type windowsTaskReload struct {
	Schema           int
	Installation     string
	Storage          Storage
	Original, Target windowsTaskManifest
	PID              int
	Start, Version   string
}

func windowsTaskPath() string {
	return filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "service.json")
}

var windowsTaskName = func() string {
	sid, _ := currentSID()
	return fmt.Sprintf("Runmoor-%x", sha256.Sum256([]byte(sid.String())))[:24]
}

func windowsTaskXML(binary, config, sid string) string {
	args := windows.EscapeArg("run") + " --config " + windows.EscapeArg(config)
	// Task Scheduler consumes a UTF-16 BSTR, not the UTF-8 file bytes. Leave
	// encoding unspecified so the XML declaration cannot contradict that input.
	return `<Task version="1.3" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><Triggers><LogonTrigger><Enabled>true</Enabled><UserId>` + xmlText(sid) + `</UserId></LogonTrigger></Triggers><Principals><Principal id="Runmoor"><UserId>` + xmlText(sid) + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals><Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><AllowHardTerminate>true</AllowHardTerminate><StartWhenAvailable>true</StartWhenAvailable><Enabled>true</Enabled><ExecutionTimeLimit>PT0S</ExecutionTimeLimit></Settings><Actions Context="Runmoor"><Exec><Command>` + xmlText(binary) + `</Command><Arguments>` + xmlText(args) + `</Arguments></Exec></Actions></Task>`
}

type windowsTaskProjection struct {
	XMLName  xml.Name `xml:"Task"`
	Triggers struct {
		Items []struct {
			XMLName         xml.Name
			Enabled, UserId string
		} `xml:",any"`
	} `xml:"Triggers"`
	Principals struct {
		Items []struct{ UserId, LogonType, RunLevel string } `xml:"Principal"`
	} `xml:"Principals"`
	Actions struct {
		Context string `xml:"Context,attr"`
		Items   []struct {
			XMLName                              xml.Name
			Command, Arguments, WorkingDirectory string
		} `xml:",any"`
	} `xml:"Actions"`
	Settings struct{ MultipleInstancesPolicy, DisallowStartIfOnBatteries, StopIfGoingOnBatteries, Enabled, ExecutionTimeLimit string } `xml:"Settings"`
}

func validWindowsTaskXML(data []byte, m windowsTaskManifest) bool {
	var actual, expected windowsTaskProjection
	if xml.Unmarshal(data, &actual) != nil || xml.Unmarshal([]byte(m.XML), &expected) != nil {
		slog.Warn("windows_task_definition_invalid", "reason", "xml")
		return false
	}
	// Task Scheduler may export an equivalent account name instead of its SID.
	// Resolve identities through Windows rather than accepting textual aliases.
	canonicalSID := func(value string) string {
		if sid, err := windows.StringToSid(value); err == nil {
			return sid.String()
		}
		if sid, _, _, err := windows.LookupSID("", value); err == nil {
			return sid.String()
		}
		return value
	}
	for i := range actual.Principals.Items {
		actual.Principals.Items[i].UserId = canonicalSID(actual.Principals.Items[i].UserId)
	}
	for i := range actual.Triggers.Items {
		actual.Triggers.Items[i].UserId = canonicalSID(actual.Triggers.Items[i].UserId)
	}
	// Task Scheduler omits schema defaults on export. Normalize only those defaults;
	// explicit disabling and elevation continue to fail ownership verification.
	for i := range actual.Triggers.Items {
		if actual.Triggers.Items[i].Enabled == "" {
			actual.Triggers.Items[i].Enabled = "true"
		}
	}
	for i := range actual.Principals.Items {
		if actual.Principals.Items[i].RunLevel == "" {
			actual.Principals.Items[i].RunLevel = "LeastPrivilege"
		}
	}
	if actual.Settings.Enabled == "" {
		actual.Settings.Enabled = "true"
	}
	a, _ := json.Marshal(actual)
	b, _ := json.Marshal(expected)
	if !bytes.Equal(a, b) {
		for name, pair := range map[string][2]any{"root": {actual.XMLName, expected.XMLName}, "triggers": {actual.Triggers, expected.Triggers}, "principal": {actual.Principals, expected.Principals}, "actions": {actual.Actions, expected.Actions}, "settings": {actual.Settings, expected.Settings}} {
			if !reflect.DeepEqual(pair[0], pair[1]) {
				slog.Warn("windows_task_definition_mismatch", "component", name)
			}
		}
	}
	return bytes.Equal(a, b)
}
func readWindowsTask() (windowsTaskManifest, error) {
	var m windowsTaskManifest
	b, e := readPrivate(windowsTaskPath(), 64<<10)
	if e != nil {
		return m, e
	}
	sid, e := currentSID()
	if e != nil || json.Unmarshal(b, &m) != nil || m.Schema != 1 || m.SID != sid.String() || !filepath.IsAbs(m.Binary) || !filepath.IsAbs(m.Config) || m.XML != windowsTaskXML(m.Binary, m.Config, m.SID) {
		return m, hostOwnership()
	}
	return m, nil
}
func taskPS(ctx context.Context, executor CommandExecutor, script string) ([]byte, error) {
	args := powershell(`[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false); try { ` + script + ` } catch { [Console]::Out.WriteLine('RUNMOOR_TASK_HRESULT=' + [string]$_.Exception.HResult); $native=[regex]::Match(([string]$_+' '+$_.FullyQualifiedErrorId),'0x[0-9a-fA-F]{8}'); if($native.Success){[Console]::Out.WriteLine('RUNMOOR_TASK_NATIVE='+$native.Value)}; [Console]::Out.WriteLine('RUNMOOR_TASK_TYPE='+$_.Exception.GetType().FullName); exit 1 }`)
	out, e := executor.Run(ctx, args[0], args[1:], minimalEnv(), nil)
	if e != nil {
		// Retain a numeric failure class only. Task XML, native output and paths
		// never cross into operator diagnostics, including PowerShell exceptions.
		code := regexp.MustCompile(`RUNMOOR_TASK_HRESULT=(-?[0-9]+)`).FindSubmatch(out)
		if len(code) == 2 {
			slog.Warn("windows_task_native_failure", "hresult", string(code[1]))
		}
		for _, pattern := range []string{`RUNMOOR_TASK_NATIVE=(0x[0-9a-fA-F]{8})`, `RUNMOOR_TASK_TYPE=([A-Za-z.]+)`} {
			if classification := regexp.MustCompile(pattern).FindSubmatch(out); len(classification) == 2 {
				slog.Warn("windows_task_failure_class", "class", string(classification[1]))
			}
		}
		return nil, problem(ErrDependency, "Windows scheduled task operation failed.", "Inspect the current user's Runmoor task in Task Scheduler and retry. Existing definitions and state have been preserved.")
	}
	return bytes.TrimPrefix(out, []byte{239, 187, 191}), nil
}
func inspectWindowsTask(ctx context.Context, executor CommandExecutor, m windowsTaskManifest) error {
	// Export declares UTF-16 although the process transport is UTF-8. Serialize
	// the document element to preserve XML content without a false declaration.
	b, e := taskPS(ctx, executor, `([xml](Export-ScheduledTask -TaskName `+psQuote(windowsTaskName())+` -TaskPath '\')).DocumentElement.OuterXml`)
	if e != nil {
		return e
	}
	if !validWindowsTaskXML(b, m) {
		return problem(ErrOwnership, "The scheduled task definition changed.", "Preserve external edits and reconcile the task with the installed Runmoor definition before retrying.")
	}
	return nil
}
func registerWindowsTask(ctx context.Context, executor CommandExecutor, m windowsTaskManifest, replace bool) error {
	tmp := filepath.Join(filepath.Dir(windowsTaskPath()), ".task-"+newID()+".xml")
	if e := writePrivateExclusive(tmp, []byte(m.XML)); e != nil {
		return e
	}
	defer os.Remove(tmp)
	force := ""
	if replace {
		force = " -Force"
	}
	_, e := taskPS(ctx, executor, `Register-ScheduledTask -TaskName `+psQuote(windowsTaskName())+` -TaskPath '\' -Xml ([IO.File]::ReadAllText(`+psQuote(tmp)+`))`+force+` | Out-Null`)
	return e
}
func windowsService(ctx context.Context, action, path string, c Config, executor CommandExecutor) (result error) {
	defer func() {
		if result != nil {
			slog.Warn("windows_service_failed", "action", action, "code", classify(result, ErrDependency, "Service failed.", "Inspect Task Scheduler.").Code)
		} else {
			slog.Info("windows_service_completed", "action", action)
		}
	}()
	if e := windowsPlatform(ctx); e != nil {
		return e
	}
	if e := privateDir(filepath.Dir(windowsTaskPath())); e != nil {
		return e
	}
	lock, e := lockState(windowsTaskPath() + ".lock")
	if e != nil {
		return e
	}
	defer unlockState(lock)
	m, e := readWindowsTask()
	if action == "install" {
		if !os.IsNotExist(e) {
			return problem(ErrConfig, "A service definition exists or cannot be verified.", "Reconcile or uninstall the existing task before installation.")
		}
		binary, e := os.Executable()
		if e != nil {
			return e
		}
		sid, e := currentSID()
		if e != nil {
			return e
		}
		m = windowsTaskManifest{Schema: 1, SID: sid.String(), Binary: binary, Config: path}
		m.XML = windowsTaskXML(binary, path, m.SID)
		body, _ := json.Marshal(m)
		if e = writePrivateExclusive(windowsTaskPath(), body); e != nil {
			return e
		}
		// Keep the manifest after an uncertain registration result. It is recovery
		// evidence, never permission to overwrite a task with the same name.
		return registerWindowsTask(ctx, executor, m, false)
	}
	if e != nil {
		return e
	}
	if m.Config != path {
		return problem(ErrConfig, "The installed task uses a different configuration.", "Select its original absolute --config path.")
	}
	if e = inspectWindowsTask(ctx, executor, m); e != nil {
		return e
	}
	switch action {
	case "start":
		if j, err := readWindowsTaskReload(); err == nil {
			if j.Storage != c.Storage || j.Target.Config != path {
				return hostOwnership()
			}
			if _, err = SendControl(ctx, c, ControlRequest{Action: "status"}); err == nil {
				return hostPending()
			}
			store, err := OpenStore(c)
			if err != nil {
				return err
			}
			snapshot := store.View()
			store.Close()
			_, active, _ := usage(snapshot)
			if snapshot.Installation != j.Installation || !snapshot.Stopping || active != 0 || pendingCleanup(snapshot) != 0 {
				return hostPending()
			}
			if err = os.Remove(windowsReloadPath()); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		_, e = taskPS(ctx, executor, `Start-ScheduledTask -TaskName `+psQuote(windowsTaskName())+` -TaskPath '\'`)
		return e
	case "stop", "uninstall":
		response, err := SendControl(ctx, c, ControlRequest{Action: "status"})
		if err == nil && response.Status != nil {
			if _, e = SendControl(ctx, c, ControlRequest{Action: "stop"}); e != nil {
				return e
			}
			if e = waitStopped(ctx, c, ""); e != nil {
				return e
			}
		} else {
			store, err := OpenStore(c)
			if err != nil {
				return err
			}
			snapshot := store.View()
			store.Close()
			_, active, _ := usage(snapshot)
			if pendingCleanup(snapshot) > 0 || active > 0 {
				return hostPending()
			}
		}
		if e = inspectWindowsTask(ctx, executor, m); e != nil {
			return e
		}
		if action == "uninstall" {
			_, e = taskPS(ctx, executor, `Unregister-ScheduledTask -TaskName `+psQuote(windowsTaskName())+` -TaskPath '\' -Confirm:$false`)
			if e != nil {
				return e
			}
			return os.Remove(windowsTaskPath())
		}
		return nil
	default:
		return problem(ErrConfig, "Unknown service action.", "Use install, start, stop or uninstall.")
	}
}
func windowsReloadPath() string { return windowsTaskPath() + ".reload.json" }
func readWindowsTaskReload() (*windowsTaskReload, error) {
	b, e := readPrivate(windowsReloadPath(), 256<<10)
	if e != nil {
		return nil, e
	}
	var j windowsTaskReload
	if json.Unmarshal(b, &j) != nil || j.Schema != 1 || !validID(j.Installation) || j.PID <= 0 || j.Start == "" || !semver.IsValid("v"+j.Version) {
		return nil, hostOwnership()
	}
	for _, m := range []windowsTaskManifest{j.Original, j.Target} {
		sid, e := currentSID()
		if e != nil || m.Schema != 1 || m.SID != sid.String() || m.XML != windowsTaskXML(m.Binary, m.Config, m.SID) || !filepath.IsAbs(m.Binary) || !filepath.IsAbs(m.Config) {
			return nil, hostOwnership()
		}
	}
	if j.Original.Config != j.Target.Config {
		return nil, hostOwnership()
	}
	return &j, nil
}
func windowsReload(ctx context.Context, path string, c Config) (result error) {
	defer func() {
		if result != nil {
			slog.Warn("windows_service_reload_failed", "code", classify(result, ErrControl, "Reload failed.", "Inspect the task and retry.").Code)
		}
	}()
	if _, e := os.Lstat(windowsTaskPath()); os.IsNotExist(e) {
		_, e = SendControl(ctx, c, ControlRequest{Action: "reload"})
		return e
	}
	lock, e := lockState(windowsTaskPath() + ".lock")
	if e != nil {
		return e
	}
	defer unlockState(lock)
	response, pid, e := sendControlPeer(ctx, c, ControlRequest{Action: "status"}, 0, true)
	j, je := readWindowsTaskReload()
	if je != nil && !os.IsNotExist(je) {
		return je
	}
	if j == nil {
		if e != nil {
			return e
		}
		m, err := readWindowsTask()
		if os.IsNotExist(err) {
			_, err = SendControl(ctx, c, ControlRequest{Action: "reload"})
			return err
		}
		if err != nil {
			return err
		}
		if m.Config != path {
			return hostOwnership()
		}
		if err = inspectWindowsTask(ctx, OSCommand{}, m); err != nil {
			return err
		}
		if response.Status == nil {
			return hostPending()
		}
		if semver.Compare("v"+response.Status.Version, "v"+Version) >= 0 {
			_, err = SendControl(ctx, c, ControlRequest{Action: "reload"})
			return err
		}
		// Native image and exact command line tie this pipe peer to the registered
		// task invocation before a manager-only replacement is permitted.
		if err = verifyWindowsManager(ctx, pid, m); err != nil {
			return err
		}
		snapshot, err := ReadSnapshot(c)
		if err != nil {
			return err
		}
		if _, err = validateReloadCandidate(ctx, c, snapshot, resolveDockerCapacity, defaultDriver, NewGitHub); err != nil {
			return err
		}
		binary, err := os.Executable()
		if err != nil {
			return err
		}
		target := m
		target.Binary = binary
		target.XML = windowsTaskXML(binary, path, m.SID)
		start, err := tartRunProcessStartIdentity(pid)
		if err != nil {
			return err
		}
		j = &windowsTaskReload{Schema: 1, Installation: snapshot.Installation, Storage: c.Storage, Original: m, Target: target, PID: pid, Start: start, Version: Version}
		body, _ := json.Marshal(j)
		if err = writePrivateExclusive(windowsReloadPath(), body); err != nil {
			return err
		}
	}
	if j.Target.Config != path || j.Storage != c.Storage || j.Version != Version {
		return hostOwnership()
	}
	m, err := readWindowsTask()
	if err != nil {
		return err
	}
	if m != j.Original && m != j.Target {
		return hostOwnership()
	}
	if err = inspectWindowsTask(ctx, OSCommand{}, m); err != nil {
		if m != j.Original || inspectWindowsTask(ctx, OSCommand{}, j.Target) != nil {
			return err
		}
		body, _ := json.Marshal(j.Target)
		old, _ := json.Marshal(m)
		if err = replaceReloadFile(windowsTaskPath(), old, 64<<10, body); err != nil {
			return err
		}
		m = j.Target
	}
	if m == j.Original {
		if err = registerWindowsTask(ctx, OSCommand{}, j.Target, true); err != nil {
			return err
		}
		b, _ := json.Marshal(j.Target)
		old, _ := json.Marshal(m)
		if err = replaceReloadFile(windowsTaskPath(), old, 64<<10, b); err != nil {
			return err
		}
	}
	if err = inspectWindowsTask(ctx, OSCommand{}, j.Target); err != nil {
		return err
	}
	if alive, err := tartRunProcessAlive(j.PID, j.Start); err != nil {
		return err
	} else if alive {
		snapshot, err := ReadSnapshot(c)
		if err != nil || snapshot.Installation != j.Installation || snapshot.Stopping {
			return hostPending()
		}
		if err = verifyWindowsManager(ctx, j.PID, j.Original); err != nil {
			return err
		}
		if err = (nativeHost{}).Stop(HostProcess{PID: j.PID, Group: j.PID, Start: j.Start}); err != nil {
			return err
		}
	}
	for {
		alive, err := tartRunProcessAlive(j.PID, j.Start)
		if err != nil {
			return err
		}
		if !alive {
			break
		}
		if !waitContext(ctx, 50*time.Millisecond) {
			return ctx.Err()
		}
	}
	if _, err = taskPS(ctx, OSCommand{}, `Start-ScheduledTask -TaskName `+psQuote(windowsTaskName())+` -TaskPath '\'`); err != nil {
		return err
	}
	for {
		out, peer, err := sendControlPeer(ctx, c, ControlRequest{Action: "status"}, 0, true)
		if err == nil && out.Status != nil && out.Status.Version == Version {
			if err = verifyWindowsManager(ctx, peer, j.Target); err != nil {
				return err
			}
			start, err := tartRunProcessStartIdentity(peer)
			if err != nil {
				return err
			}
			if _, _, err = sendControlIdentity(ctx, c, ControlRequest{Action: "reload"}, peer, start, true); err != nil {
				return err
			}
			return os.Remove(windowsReloadPath())
		}
		if !waitContext(ctx, time.Second) {
			return ctx.Err()
		}
	}
}
func verifyWindowsManager(ctx context.Context, pid int, m windowsTaskManifest) error {
	engines, err := taskPS(ctx, OSCommand{}, `$service=New-Object -ComObject 'Schedule.Service'; $service.Connect(); ConvertTo-Json -Compress -InputObject @($service.GetFolder('\').GetTask(`+psQuote(windowsTaskName())+`).GetInstances(0) | ForEach-Object { $_.EnginePID })`)
	var ids []uint32
	if err != nil || json.Unmarshal(engines, &ids) != nil || len(ids) != 1 || ids[0] == 0 {
		return hostOwnership()
	}
	parent, err := taskPS(ctx, OSCommand{}, fmt.Sprintf(`(Get-CimInstance Win32_Process -Filter 'ProcessId = %d').ParentProcessId | ConvertTo-Json -Compress`, pid))
	var parentID uint32
	if err != nil || json.Unmarshal(parent, &parentID) != nil || ids[0] != uint32(pid) && ids[0] != parentID {
		return hostOwnership()
	}
	b, e := taskPS(ctx, OSCommand{}, fmt.Sprintf(`(Get-CimInstance Win32_Process -Filter 'ProcessId = %d').CommandLine | ConvertTo-Json -Compress`, pid))
	var line string
	if e != nil || json.Unmarshal(b, &line) != nil {
		return hostOwnership()
	}
	args, e := windows.DecomposeCommandLine(line)
	if e != nil || len(args) != 4 || !strings.EqualFold(args[0], m.Binary) || args[1] != "run" || args[2] != "--config" || args[3] != m.Config {
		return hostOwnership()
	}
	return nil
}
func windowsStartup(ctx context.Context, path string, c Config) (Config, bool, *serviceReloadJournal, error) {
	j, e := readWindowsTaskReload()
	if os.IsNotExist(e) {
		return c, false, nil, nil
	}
	if e != nil {
		return c, false, nil, e
	}
	exe, e := os.Executable()
	if e != nil || path != j.Target.Config || c.Storage != j.Storage || exe != j.Target.Binary && exe != j.Original.Binary {
		return c, false, nil, hostOwnership()
	}
	snapshot, e := ReadSnapshot(c)
	if e != nil || snapshot.Installation != j.Installation {
		return c, false, nil, hostOwnership()
	}
	// Candidate TOML is not accepted merely because task replacement occurred.
	// Retain committed configuration, active generations and Stop until reload.
	return snapshot.Requested, true, nil, ctx.Err()
}
func init() {
	windowsServiceOperation = windowsService
	windowsReloadOperation = windowsReload
	windowsStartupOperation = windowsStartup
	validateWindowsDockerHost = func(ctx context.Context) error {
		if e := windowsPlatform(ctx); e != nil {
			return e
		}
		key, e := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
		if e != nil {
			return e
		}
		defer key.Close()
		edition, _, e := key.GetStringValue("EditionID")
		if e != nil || !(strings.HasPrefix(edition, "Professional") || strings.HasPrefix(edition, "Enterprise")) {
			return problem(ErrPlatform, "Windows containers require Windows 11 Pro or Enterprise.", "Enable Hyper-V and Containers, and select Windows containers in Docker Desktop.")
		}
		return nil
	}
}
