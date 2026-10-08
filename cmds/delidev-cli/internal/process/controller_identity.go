// SPDX-License-Identifier: Apache-2.0
package process

import (
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// ControllerIdentity observes only the original infrastructure controller. It
// does not own a process scope or prove termination of any native descendants.
type ControllerIdentity struct {
	PID   int    `json:"pid"`
	Birth string `json:"birth"`
}
type ControllerObservation string

const (
	ControllerAlive   ControllerObservation = "same-original-alive"
	ControllerExited  ControllerObservation = "original-exited"
	ControllerUnknown ControllerObservation = "unknown"
)

func (identity ControllerIdentity) Valid() bool {
	pidLimit := int64(2147483647)
	if runtime.GOOS == "windows" {
		pidLimit = 4294967295
	}
	if identity.PID <= 0 || int64(identity.PID) > pidLimit {
		return false
	}
	switch runtime.GOOS {
	case "linux":
		parts := strings.Split(identity.Birth, ":")
		if len(parts) != 2 || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(parts[0]) {
			return false
		}
		ticks, err := strconv.ParseUint(parts[1], 10, 64)
		return err == nil && ticks > 0 && strconv.FormatUint(ticks, 10) == parts[1]
	case "darwin":
		if !regexp.MustCompile(`^[1-9][0-9]*\.[0-9]{6}$`).MatchString(identity.Birth) {
			return false
		}
		_, err := strconv.ParseUint(strings.Split(identity.Birth, ".")[0], 10, 64)
		return err == nil
	case "windows":
		parts := strings.Split(identity.Birth, ":")
		if len(parts) != 2 {
			return false
		}
		for _, part := range parts {
			value, err := strconv.ParseUint(part, 10, 32)
			if err != nil || strconv.FormatUint(value, 10) != part {
				return false
			}
		}
		return identity.Birth != "0:0"
	default:
		return false
	}
}

func CaptureControllerIdentity() (ControllerIdentity, error) {
	birth, err := controllerBirth(os.Getpid())
	identity := ControllerIdentity{PID: os.Getpid(), Birth: birth}
	if err == nil && !identity.Valid() {
		err = os.ErrInvalid
	}
	return identity, err
}

// ObserveControllerIdentity never signals or adopts the observed process.
// Inspection errors cannot become positive original-exit evidence.
func ObserveControllerIdentity(identity ControllerIdentity) ControllerObservation {
	return observeControllerIdentity(identity, controllerBirth, controllerAbsent)
}
func observeControllerIdentity(identity ControllerIdentity, birth func(int) (string, error), absent func(int, error) bool) ControllerObservation {
	if !identity.Valid() {
		return ControllerUnknown
	}
	current, err := birth(identity.PID)
	if err != nil {
		if absent(identity.PID, err) {
			return ControllerExited
		}
		return ControllerUnknown
	}
	if !(ControllerIdentity{PID: identity.PID, Birth: current}).Valid() {
		return ControllerUnknown
	}
	if current != identity.Birth {
		return ControllerExited
	}
	return controllerPresent(identity)
}
