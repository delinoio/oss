//go:build !darwin && !linux

package runmoor

func renameServiceDefinitionNoReplace(string, string) error {
	return unsupported()
}
