package userservice

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

func launch(ctx context.Context, args ...string) ([]byte, error) {
	return nativeCommand(ctx, "/bin/launchctl", args, nativeEnv("darwin"), nil)
}
func launchDomain(s Spec) string { return "gui/" + s.User }
func launchMissing(err error) bool {
	var e interface{ ExitCode() int }
	return errors.As(err, &e) && e.ExitCode() == 113
}
func launchInspection(raw []byte, s Spec) (int, error) {
	// launchctl print is explicitly a debugging interface, not a stable format.
	// Accept only the positively recognized complete ownership fields; fail closed
	// on changed/ambiguous formatting. Replace this parser if Apple supplies a
	// stable query API for inactive cached LaunchAgent definitions.
	fields := map[string]string{}
	args := []string{}
	inArgs := false
	argsSeen := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if inArgs {
			if line == "}" {
				inArgs = false
				continue
			}
			if line == "" {
				return 0, failure()
			}
			args = append(args, line)
			continue
		}
		if line == "arguments = {" {
			if argsSeen {
				return 0, failure()
			}
			argsSeen = true
			inArgs = true
			continue
		}
		for _, key := range []string{"path", "program", "pid"} {
			if strings.HasPrefix(line, key+" = ") {
				if _, ok := fields[key]; ok {
					return 0, failure()
				}
				fields[key] = strings.TrimPrefix(line, key+" = ")
			}
		}
	}
	expected := append([]string{s.Binary}, s.args()...)
	if inArgs || fields["path"] != s.DefinitionPath || fields["program"] != s.Binary || len(args) != len(expected) {
		return 0, failure()
	}
	for i := range args {
		if args[i] != expected[i] {
			return 0, failure()
		}
	}
	pid := 0
	if text, ok := fields["pid"]; ok {
		n, err := strconv.Atoi(text)
		if err != nil || n <= 0 {
			return 0, failure()
		}
		pid = n
	}
	return pid, nil
}
func (nativeBackend) Inspect(ctx context.Context, s Spec) (Observation, error) {
	present, err := readDefinition(s)
	if err != nil {
		return Observation{}, err
	}
	if _, err := launch(ctx, "print", launchDomain(s)); err != nil {
		return Observation{}, unavailable()
	}
	raw, err := launch(ctx, "print", launchDomain(s)+"/"+s.Name)
	pid := 0
	if err == nil {
		if !present {
			return Observation{}, failure()
		}
		pid, err = launchInspection(raw, s)
		if err != nil {
			return Observation{}, err
		}
	} else if !launchMissing(err) {
		return Observation{}, unavailable()
	}
	if !present {
		return Observation{}, nil
	}
	disabled, err := launch(ctx, "print-disabled", launchDomain(s))
	if err != nil {
		return Observation{}, unavailable()
	}
	enabled := true
	seen := false
	for _, line := range strings.Split(string(disabled), "\n") {
		line = strings.TrimSpace(line)
		prefix := `"` + s.Name + `" => `
		if strings.HasPrefix(line, prefix) {
			if seen {
				return Observation{}, failure()
			}
			seen = true
			value := strings.TrimPrefix(line, prefix)
			if value != "true" && value != "false" && value != "enabled" && value != "disabled" {
				return Observation{}, failure()
			}
			enabled = value == "false" || value == "enabled"
		}
	}
	return Observation{Present: true, Enabled: enabled, PID: pid}, nil
}
func (nativeBackend) Install(ctx context.Context, s Spec) error {
	if _, e := launch(ctx, "disable", launchDomain(s)+"/"+s.Name); e != nil {
		return unavailable()
	}
	return createDefinition(s)
}
func (nativeBackend) Enable(ctx context.Context, s Spec) error {
	_, e := launch(ctx, "enable", launchDomain(s)+"/"+s.Name)
	if e != nil {
		return unavailable()
	}
	return nil
}
func (nativeBackend) Start(ctx context.Context, s Spec) error {
	_, err := launch(ctx, "print", launchDomain(s)+"/"+s.Name)
	if launchMissing(err) {
		if _, err = launch(ctx, "bootstrap", launchDomain(s), s.DefinitionPath); err != nil {
			return unavailable()
		}
	} else if err != nil {
		return unavailable()
	}
	// launchd's default restart throttle can delay a repeated launch for ten
	// seconds. Allow that bounded manager barrier to complete once; a timeout
	// still retains uncertainty and must never trigger another native write.
	if _, err = nativeCommandTimeout(ctx, "/bin/launchctl", []string{"kickstart", launchDomain(s) + "/" + s.Name}, nativeEnv("darwin"), nil, 15*time.Second); err != nil {
		return unavailable()
	}
	return nil
}
func (nativeBackend) Disable(ctx context.Context, s Spec) error {
	_, e := launch(ctx, "disable", launchDomain(s)+"/"+s.Name)
	if e != nil {
		return unavailable()
	}
	return nil
}
func (nativeBackend) Remove(ctx context.Context, s Spec) error {
	raw, err := launch(ctx, "print", launchDomain(s)+"/"+s.Name)
	if err == nil {
		pid, e := launchInspection(raw, s)
		if e != nil || pid != 0 {
			return failure()
		}
		if _, e := launch(ctx, "bootout", launchDomain(s)+"/"+s.Name); e != nil {
			return unavailable()
		}
	} else if !launchMissing(err) {
		return unavailable()
	}
	if _, e := launch(ctx, "print", launchDomain(s)+"/"+s.Name); !launchMissing(e) {
		return failure()
	}
	return removeDefinition(s)
}
