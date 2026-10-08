// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"testing"
	"time"
)

func TestExtendedUnaryBudgetsKeepReadAndNativeControls(t *testing.T) {
	for _, tc := range []struct {
		command string
		args    []string
		seconds int
	}{
		{"account", []string{"oauth", "complete"}, 35}, {"update", []string{"check"}, 35},
		{"account", []string{"validate"}, 50}, {"provider", []string{"discover"}, 50},
		{"account", nil, 0}, {"account", []string{"oauth"}, 0},
		{"account", []string{"oauth", "status"}, 0}, {"account", []string{"oauth", "start"}, 0}, {"account", []string{"oauth", "cancel"}, 0},
		{"account", []string{"connect"}, 0}, {"account", []string{"status"}, 0}, {"account", []string{"login"}, 0},
		{"account", []string{"refresh"}, 0}, {"account", []string{"logout"}, 0},
		{"provider", []string{"list"}, 0}, {"provider", []string{"presets"}, 0},
		{"model", []string{"native-discover"}, 0}, {"update", []string{"get"}, 0},
		{"update", []string{"worker-request"}, 0}, {"update", []string{"cancel"}, 0},
		{"events", nil, 0}, {"session", []string{"terminal", "output"}, 0},
		{"session", []string{"forward", "start"}, 0}, {"desktop", []string{"update"}, 0},
	} {
		t.Run(tc.command+"/"+joinDeadlineArgs(tc.args), func(t *testing.T) {
			if got := extendedUnaryBudget(tc.command, tc.args); got != time.Duration(tc.seconds)*time.Second {
				t.Fatalf("budget=%s expected%dseconds", got, tc.seconds)
			}
		})
	}
	_, transport := rpc.HTTPClient()
	defer transport.CloseIdleConnections()
	if transport.ResponseHeaderTimeout != 15*time.Second {
		t.Fatal("ordinary header budget changed")
	}
}
func joinDeadlineArgs(args []string) string {
	var value string
	for _, arg := range args {
		value += "-" + arg
	}
	return value
}
