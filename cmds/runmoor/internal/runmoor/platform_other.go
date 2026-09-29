//go:build !darwin && !linux

package runmoor

import (
	"os"
	"os/exec"
)

func unsupported() error {
	return problem(ErrPlatform, "Runmoor supports macOS arm64 and Ubuntu amd64/arm64 only.", "Run the matching release on a supported host.")
}
func privateDir(string) error                               { return unsupported() }
func openPrivate(string, int) (*os.File, error)             { return nil, unsupported() }
func readPrivate(string, int64) ([]byte, error)             { return nil, unsupported() }
func privateVMDirectory(string) (os.FileInfo, error)        { return nil, unsupported() }
func openTartVMDirectory(string) (*os.File, error)          { return nil, unsupported() }
func openTartVMFileAt(*os.File, string) (*os.File, error)   { return nil, unsupported() }
func readTartVMOwnerMarker(*os.File, int64) ([]byte, error) { return nil, unsupported() }
func publishTartVMOwnerMarker(*os.File, []byte) error       { return unsupported() }
func syncPrivateDir(string) error                           { return unsupported() }
func lockTartVMConfig(string) (*os.File, error)             { return nil, unsupported() }
func lockTartVMConfigAt(*os.File) (*os.File, error)         { return nil, unsupported() }
func lockTartCreationHome(string) (*os.File, error)         { return nil, unsupported() }
func renameTartVMNoReplace(string, string) error            { return unsupported() }
func lockState(string) (*os.File, error)                    { return nil, unsupported() }
func unlockState(*os.File)                                  {}
func freeDisk(string) (uint64, error)                       { return 0, unsupported() }
func detach(*exec.Cmd)                                      {}
func interruptProcess(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
func tartRunProcessAlive(int, string) (bool, error)   { return false, unsupported() }
func tartRunProcessStartIdentity(int) (string, error) { return "", unsupported() }
