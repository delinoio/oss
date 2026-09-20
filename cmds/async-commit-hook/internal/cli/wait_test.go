package cli

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/async-commit-hook/internal/core"
)

func TestWaitTimeoutRejectsDurationOverflow(t *testing.T) {
	for _, tc := range []struct {
		value   string
		seconds int64
		code    string
	}{
		{"", 60, ""}, {"0", 0, ""}, {"60", 60, ""},
		{strconv.FormatInt(maxWaitSeconds, 10), maxWaitSeconds, ""},
		{strconv.FormatInt(maxWaitSeconds+1, 10), 0, "invalid-timeout"},
		{"9223372036854775807", 0, "invalid-timeout"},
		{"9223372036854775808", 0, "invalid-number"}, {"-1", 0, "invalid-number"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			args := []string{"wait"}
			if tc.value != "" {
				args = append(args, "--timeout", tc.value)
			}
			o, err := parse(args)
			if err != nil {
				t.Fatal(err)
			}
			duration, err := o.waitTimeout()
			if tc.code == "" {
				if err != nil || duration < 0 || int64(duration/time.Second) != tc.seconds {
					t.Fatal(duration, err)
				}
			} else {
				var typed *core.Error
				if !errors.As(err, &typed) || typed.Code != tc.code || typed.Exit != 2 {
					t.Fatal(err)
				}
			}
		})
	}
}
