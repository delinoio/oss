//go:build !darwin

package runmoor

import (
	"context"
	"os"
)

type nativeHost struct{}

func hostPlatform(context.Context) error {
	return problem(ErrPlatform, "Host execution requires macOS 14+ Apple Silicon.", "Select Docker on Ubuntu or use a supported Mac.")
}
func (nativeHost) Platform(ctx context.Context) error { return hostPlatform(ctx) }
func (nativeHost) Version(context.Context, *os.Root, string) error {
	return hostPlatform(context.Background())
}
func (nativeHost) Launch(context.Context, *os.Root, HostBootstrap, func(HostProcess) error) error {
	return hostPlatform(context.Background())
}
func (nativeHost) Alive(HostProcess) (bool, error) { return false, hostPlatform(context.Background()) }
func (nativeHost) Group(HostProcess) ([]HostProcess, error) {
	return nil, hostPlatform(context.Background())
}
func (nativeHost) Stop(HostProcess) error { return hostPlatform(context.Background()) }
func hostSupervise() int                  { return 2 }
