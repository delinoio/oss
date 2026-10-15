//go:build !darwin && !linux && !windows

package runmoor

func hostCapacity() (Resources, error) {
	return Resources{}, problem(ErrPlatform, "Automatic capacity detection requires a supported host.", "Use Ubuntu or Apple Silicon macOS.")
}
