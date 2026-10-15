//go:build !darwin && !linux && !windows

package runmoor

func renameServiceDefinitionNoReplace(string, string) error {
	return unsupported()
}
