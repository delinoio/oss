//go:build !darwin

package servicecontrol

import (
	"context"
	"fmt"
)

type unsupportedManager struct{}

func newManager(_ *managerOptions) (Manager, error) {
	return &unsupportedManager{}, nil
}

func (manager *unsupportedManager) Install(_ context.Context) error {
	return fmt.Errorf("%w: install", ErrUnsupportedPlatform)
}

func (manager *unsupportedManager) Uninstall(_ context.Context) error {
	return fmt.Errorf("%w: uninstall", ErrUnsupportedPlatform)
}

func (manager *unsupportedManager) Start(_ context.Context) error {
	return fmt.Errorf("%w: start", ErrUnsupportedPlatform)
}

func (manager *unsupportedManager) Stop(_ context.Context) error {
	return fmt.Errorf("%w: stop", ErrUnsupportedPlatform)
}

func (manager *unsupportedManager) Status(_ context.Context) (Summary, error) {
	return Summary{DaemonHealth: DaemonHealthError}, fmt.Errorf("%w: status", ErrUnsupportedPlatform)
}
