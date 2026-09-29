//go:build !darwin

package runmoor

func launchdProcessCommandLine(int) ([]string, error) {
	return nil, errInvalidServiceDefinition
}
