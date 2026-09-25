//go:build !darwin && !windows && !linux

package credentials

func newNative() (nativeStore, error) { return nil, unavailable() }
