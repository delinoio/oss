package runmoor

import (
	"context"
	"strings"
)

func hasDockerArtifacts(s Snapshot) bool {
	for _, a := range s.Artifacts {
		if a.Backend == Docker {
			return true
		}
	}
	return false
}

func dockerArtifactOwnership() error {
	return problem(ErrOwnership, "Managed Docker artifacts cannot change their original engine authority.", "Remove affected Docker pools and complete artifact cleanup on the original engine before changing its endpoint. Preserve legacy artifacts whose original endpoint is unknown.")
}

func validArtifactDockerEndpoint(endpoint string) bool {
	return strings.HasPrefix(endpoint, "unix:///") && !strings.ContainsAny(endpoint, "\x00\n\r")
}

// The pin is private snapshot state, not an authored selector or status field.
// Legacy records cannot acquire origin authority from whichever engine is now
// selected: absence on that engine does not prove original resources exited.
func checkDockerArtifactEndpoint(s Snapshot, endpoint string) error {
	if !hasDockerArtifacts(s) {
		return nil
	}
	if !validArtifactDockerEndpoint(s.DockerArtifactEndpoint) || endpoint != s.DockerArtifactEndpoint {
		return dockerArtifactOwnership()
	}
	return nil
}

func guardDockerArtifactEndpoint(ctx context.Context, c Config, s Snapshot) (string, error) {
	if !hasDockerArtifacts(s) {
		return "", nil
	}
	if !validArtifactDockerEndpoint(s.DockerArtifactEndpoint) {
		return "", dockerArtifactOwnership()
	}
	endpoint, err := dockerEndpoint(ctx, c)
	if err != nil {
		return "", err
	}
	return endpoint, checkDockerArtifactEndpoint(s, endpoint)
}

func dockerArtifactConfig(c Config, s Snapshot) (Config, error) {
	if !validArtifactDockerEndpoint(s.DockerArtifactEndpoint) {
		return c, dockerArtifactOwnership()
	}
	c.DockerSocket = s.DockerArtifactEndpoint
	return c, nil
}
