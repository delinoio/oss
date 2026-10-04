//go:build !darwin && !linux

package runmoor

import "os"

func hostPrivateInfo(os.FileInfo, bool) bool             { return false }
func hostFileIdentity(os.FileInfo) string                { return "" }
func hostRenameNoReplace(*os.Root, string, string) error { return unsupported() }
