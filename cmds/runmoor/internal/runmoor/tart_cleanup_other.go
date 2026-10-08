//go:build !darwin && !linux

package runmoor

import "os"

func tartCleanupProcess() int                                             { return tartCleanupPendingExit }
func prepareTartCleanup(tartCleanupRequest, []*os.File) (*os.File, error) { return nil, unsupported() }

func confirmTartCleanupRemoved(*os.File, *os.File, string) error { return unsupported() }
