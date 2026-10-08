//go:build !linux

package runmoor

func openReloadManager(int) (reloadManagerHandle, error) { return nil, reloadFailure() }
