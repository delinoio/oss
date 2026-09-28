//go:build !darwin && !windows && !linux

package credentials

func newNativeProfile(nativeProfile) (nativeStore, error) { return nil, unavailable() }
