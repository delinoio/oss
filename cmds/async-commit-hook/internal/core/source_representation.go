package core

// This policy is fixed per OS, never inferred from current user privileges or
// original checkout settings. It therefore participates in execution identity.
type sourceRepresentation string

const (
	nativeSymlinks   sourceRepresentation = ""
	symlinkTextFiles sourceRepresentation = "git-symlink-files-v1"
)

func sourceRepresentationForOS(osName string) sourceRepresentation {
	if osName == "windows" {
		return symlinkTextFiles
	}
	return nativeSymlinks
}
func validateSourceRepresentation(r Run, osName, arch string) error {
	if sourceRepresentationForOS(osName) == symlinkTextFiles && r.Fingerprint != fingerprintForPlatform(r.Config, r.Environment, osName, arch) {
		return E("incompatible-context", "Windows source representation changed; submit ach run --commit for this SHA instead of reusing or rerunning the old attempt", 2)
	}
	return nil
}
