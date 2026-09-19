package runmoor

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// This subprocess models a live inhibitor without changing OS sleep policy.
func TestPowerInhibitorProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "runmoor-power-fixture" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestPowerRetriesWhenNewWorkStartsAfterIdle(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, recovery := range []bool{false, true} {
		name := "failure stays visible"
		if recovery {
			name = "new work acquires inhibitor"
		}
		t.Run(name, func(t *testing.T) {
			attempts := 0
			missing := filepath.Join(t.TempDir(), "missing-inhibitor")
			p := &PowerManager{command: func() (string, []string) {
				attempts++
				if attempts == 1 || !recovery {
					return missing, nil
				}
				return binary, []string{"-test.run=^TestPowerInhibitorProcess$", "--", "runmoor-power-fixture"}
			}}
			defer p.Release()
			first := p.Set(true)
			if first == nil || first.Code != ErrPower || attempts != 1 {
				t.Fatal("initial acquisition did not report its failure", first)
			}
			if warning := p.Set(true); warning != first || attempts != 1 {
				t.Fatal("continuous activity lost its warning or bypassed retry backoff", warning)
			}
			if warning := p.Set(false); warning != nil {
				t.Fatal("idle state retained an active-work warning", warning)
			}
			warning := p.Set(true)
			if attempts != 2 {
				t.Fatal("new activity silently reused an idle retry window")
			}
			if recovery {
				if warning != nil || p.cmd == nil || p.cmd.Process == nil {
					t.Fatal("new activity did not acquire its inhibitor", warning)
				}
				if warning = p.Set(true); warning != nil || attempts != 2 {
					t.Fatal("live inhibitor was not retained", warning)
				}
			} else if warning == nil || warning.Code != ErrPower {
				t.Fatal("new activity hid repeated acquisition failure", warning)
			}
			if warning := p.Set(false); warning != nil || p.cmd != nil || p.input != nil {
				t.Fatal("idle transition did not release its inhibitor", warning)
			}
		})
	}
}
