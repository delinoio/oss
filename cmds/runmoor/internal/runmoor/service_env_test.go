package runmoor

import (
	"os"
	"strings"
	"testing"
)

func commandEnvironmentMap(t *testing.T, entries []string) map[string]string {
	t.Helper()
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("environment entry has no value separator: %q", entry)
		}
		if _, exists := values[key]; exists {
			t.Fatalf("environment contains duplicate key %q", key)
		}
		values[key] = value
	}
	return values
}

func unsetTestEnvironment(t *testing.T, key string) {
	t.Helper()
	value, present := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var err error
		if present {
			err = os.Setenv(key, value)
		} else {
			err = os.Unsetenv(key)
		}
		if err != nil {
			t.Errorf("restore %s: %v", key, err)
		}
	})
}

func TestLinuxSystemctlEnvironmentPreservesOnlyProvidedSessionSelectors(t *testing.T) {
	const runtimeDirKey = "XDG_RUNTIME_DIR"
	const busAddressKey = "DBUS_SESSION_BUS_ADDRESS"
	for _, key := range []string{runtimeDirKey, busAddressKey} {
		unsetTestEnvironment(t, key)
	}
	t.Setenv("RUNMOOR_FIXTURE_PAT", "fixture-service-secret")

	for _, tc := range []struct {
		name       string
		runtimeDir *string
		busAddress *string
	}{
		{name: "both", runtimeDir: stringPointer("/run/user/1701 with space"), busAddress: stringPointer("unix:path=/run/user/1701/bus;guid=fixture")},
		{name: "runtime only", runtimeDir: stringPointer("/run/user/1702")},
		{name: "bus only", busAddress: stringPointer("unix:path=/tmp/fixture-bus")},
		{name: "neither"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{runtimeDirKey, busAddressKey} {
				unsetTestEnvironment(t, key)
			}
			if tc.runtimeDir != nil {
				t.Setenv(runtimeDirKey, *tc.runtimeDir)
			}
			if tc.busAddress != nil {
				t.Setenv(busAddressKey, *tc.busAddress)
			}

			got := commandEnvironmentMap(t, serviceCommandEnv("linux", "systemctl"))
			want := commandEnvironmentMap(t, minimalEnv())
			if tc.runtimeDir != nil {
				want[runtimeDirKey] = *tc.runtimeDir
			}
			if tc.busAddress != nil {
				want[busAddressKey] = *tc.busAddress
			}
			if len(got) != len(want) {
				t.Fatalf("environment keys = %v, want only %v", got, want)
			}
			for key, value := range want {
				if got[key] != value {
					t.Errorf("%s = %q, want %q", key, got[key], value)
				}
			}
			if _, exists := got["RUNMOOR_FIXTURE_PAT"]; exists {
				t.Fatal("fixture credential entered the service command environment")
			}
		})
	}
}

func TestSessionSelectorsAreLimitedToLinuxSystemctl(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1701")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1701/bus")
	for _, tc := range []struct {
		goos    string
		command string
		want    bool
	}{
		{goos: "linux", command: "systemctl", want: true},
		{goos: "linux", command: "launchctl"},
		{goos: "darwin", command: "systemctl"},
	} {
		t.Run(tc.goos+"/"+tc.command, func(t *testing.T) {
			got := commandEnvironmentMap(t, serviceCommandEnv(tc.goos, tc.command))
			for _, key := range []string{"XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"} {
				_, present := got[key]
				if present != tc.want {
					t.Errorf("%s present = %t, want %t", key, present, tc.want)
				}
			}
		})
	}
}

func stringPointer(value string) *string { return &value }
