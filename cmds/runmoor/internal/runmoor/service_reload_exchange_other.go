//go:build !darwin && !linux

package runmoor

import "errors"

func exchangeServiceFiles(string, string) error {
	return errors.New("conditional service replacement is unsupported on this platform")
}
