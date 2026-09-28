//go:build !darwin && !linux

package runmoor

func hostCapacity() (Resources, error) {
	return Resources{}, problem(ErrPlatform, "Automatic capacity detection requires a supported host.", "Use Ubuntu or Apple Silicon macOS.")
}
