package opencode

// Ordinary native Read failure is separate from an accepted permission
// rejection. The original closed observer must first validate every observed
// interaction; missing or unsettled replies cannot acquire this profile.
type checkpointToolErrorProfile string

const checkpointReadError checkpointToolErrorProfile = "read-error"

func checkpointHasReadError(proof *checkpointToolHistory) bool {
	for _, part := range proof.Parts {
		if part.ErrorProfile == checkpointReadError {
			return true
		}
	}
	return false
}
