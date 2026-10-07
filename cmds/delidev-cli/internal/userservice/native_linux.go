package userservice

import (
	"context"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"net"
	"os"
	"slices"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
)

func systemctl(ctx context.Context, args ...string) error {
	_, e := nativeCommand(ctx, "/usr/bin/systemctl", append([]string{"--user"}, args...), nativeEnv("linux"), nil)
	if e != nil {
		return unavailable()
	}
	return nil
}
func userBus(ctx context.Context) (*dbus.Conn, error) {
	// Only an existing same-user Unix bus is eligible. Do not let a missing
	// session fall back to dbus-launch, a remote address or a system manager.
	path, err := existingBusPath(os.Getenv("DBUS_SESSION_BUS_ADDRESS"), os.Getenv("XDG_RUNTIME_DIR"))
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, unavailable()
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || domain.OwnershipBlocks(domain.OwnershipResource, domain.NewID(), st.Uid != uint32(os.Geteuid())) {
		return nil, unavailable()
	}
	child, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(child, "unix", path)
	if err != nil {
		return nil, unavailable()
	}
	deadline, _ := child.Deadline()
	_ = conn.SetDeadline(deadline)
	bus, err := dbus.DialUnix(conn.(*net.UnixConn), dbus.WithContext(ctx))
	if err != nil {
		conn.Close()
		return nil, unavailable()
	}
	user, _ := currentUser()
	if bus.Auth([]dbus.Auth{dbus.AuthExternal(user)}) != nil || bus.Hello() != nil {
		bus.Close()
		return nil, unavailable()
	}
	_ = conn.SetDeadline(time.Time{})
	return bus, nil
}

type systemdExec struct {
	Path           string
	Args           []string
	Ignore         bool
	StartRealtime  uint64
	StartMonotonic uint64
	ExitRealtime   uint64
	ExitMonotonic  uint64
	PID            uint32
	Code           int32
	Status         int32
}

func systemdProps(ctx context.Context, bus *dbus.Conn, path dbus.ObjectPath, iface string) (map[string]dbus.Variant, error) {
	var props map[string]dbus.Variant
	err := bus.Object("org.freedesktop.systemd1", path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, iface).Store(&props)
	return props, err
}
func (nativeBackend) Inspect(ctx context.Context, s Spec) (Observation, error) {
	present, err := readDefinition(s)
	if err != nil {
		return Observation{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	bus, err := userBus(ctx)
	if err != nil {
		return Observation{}, err
	}
	defer bus.Close()
	manager := bus.Object("org.freedesktop.systemd1", "/org/freedesktop/systemd1")
	var path dbus.ObjectPath
	method := "GetUnit"
	if present {
		method = "LoadUnit"
	}
	err = manager.CallWithContext(ctx, "org.freedesktop.systemd1.Manager."+method, 0, s.Name+".service").Store(&path)
	if err != nil {
		var e dbus.Error
		if !present && errors.As(err, &e) && e.Name == "org.freedesktop.systemd1.NoSuchUnit" {
			return Observation{}, nil
		}
		return Observation{}, unavailable()
	}
	unit, err := systemdProps(ctx, bus, path, "org.freedesktop.systemd1.Unit")
	if err != nil {
		return Observation{}, unavailable()
	}
	service, err := systemdProps(ctx, bus, path, "org.freedesktop.systemd1.Service")
	if err != nil {
		return Observation{}, unavailable()
	}
	if !present {
		fragment, ok := unit["FragmentPath"].Value().(string)
		state, ok2 := unit["LoadState"].Value().(string)
		pid, ok3 := service["MainPID"].Value().(uint32)
		if ok && ok2 && ok3 && fragment == "" && state == "not-found" && pid == 0 {
			return Observation{}, nil
		}
		return Observation{}, failure()
	}
	fragment, ok := unit["FragmentPath"].Value().(string)
	if !ok || fragment != s.DefinitionPath {
		return Observation{}, failure()
	}
	drops, ok := unit["DropInPaths"].Value().([]string)
	if !ok || len(drops) != 0 {
		return Observation{}, failure()
	}
	var commands []systemdExec
	if dbus.Store([]any{service["ExecStart"].Value()}, &commands) != nil || len(commands) != 1 || commands[0].Path != s.Binary || commands[0].Ignore || !slices.Equal(commands[0].Args, append([]string{s.Binary}, s.args()...)) {
		return Observation{}, failure()
	}
	user, ok := service["User"].Value().(string)
	if !ok || (user != "" && user != s.User) {
		return Observation{}, failure()
	}
	kill, ok := service["KillMode"].Value().(string)
	if !ok || kill != "process" {
		return Observation{}, failure()
	}
	restart, ok := service["Restart"].Value().(string)
	if !ok || restart != "on-failure" {
		return Observation{}, failure()
	}
	pid, ok := service["MainPID"].Value().(uint32)
	if !ok {
		return Observation{}, failure()
	}
	var enabled string
	if manager.CallWithContext(ctx, "org.freedesktop.systemd1.Manager.GetUnitFileState", 0, s.Name+".service").Store(&enabled) != nil {
		return Observation{}, unavailable()
	}
	if enabled != "enabled" && enabled != "disabled" {
		return Observation{}, failure()
	}
	return Observation{Present: true, Enabled: enabled == "enabled", PID: int(pid)}, nil
}
func (nativeBackend) Install(ctx context.Context, s Spec) error {
	if err := createDefinition(s); err != nil {
		return err
	}
	return systemctl(ctx, "daemon-reload")
}
func (nativeBackend) Enable(ctx context.Context, s Spec) error {
	return systemctl(ctx, "enable", s.Name+".service")
}
func (nativeBackend) Start(ctx context.Context, s Spec) error {
	return systemctl(ctx, "start", s.Name+".service")
}
func (nativeBackend) Disable(ctx context.Context, s Spec) error {
	return systemctl(ctx, "disable", s.Name+".service")
}
func (nativeBackend) Remove(ctx context.Context, s Spec) error {
	if err := systemctl(ctx, "stop", s.Name+".service"); err != nil {
		return err
	}
	if err := removeDefinition(s); err != nil {
		return err
	}
	return systemctl(ctx, "daemon-reload")
}
