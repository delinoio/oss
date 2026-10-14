package worker

import (
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
)

type profileRegistryKey struct {
	root    string
	machine domain.ID
}

var (
	profileRegistriesMu sync.Mutex
	profileRegistries   = map[profileRegistryKey]*nativeprofile.Registry{}
)

// nativeProfiles resolves the Worker-local persistent native profile registry.
// The registry is shared by every native launch path of this Worker root so
// discovery, execution, Fork, compaction, Revert, Sidechat, title and review
// resolve the same semantic profiles. Server and Worker files stay local to
// their own owner; the Worker machine identity is part of profile ownership.
func nativeProfiles(config Config, machine domain.ID) (*nativeprofile.Registry, error) {
	if err := machine.Validate(); err != nil {
		return nil, err
	}
	key := profileRegistryKey{root: config.Root, machine: machine}
	profileRegistriesMu.Lock()
	defer profileRegistriesMu.Unlock()
	if registry := profileRegistries[key]; registry != nil {
		return registry, nil
	}
	// A Worker machine identity is minted by exactly one server pairing, so the
	// machine alone binds the Worker root to its server; no server identifier is
	// read from credentials, keeping every launch path on one stable key.
	registry, err := nativeprofile.Open(nativeprofile.Config{Root: config.Root, Owner: nativeprofile.OwnerWorker, Machine: machine, Logger: config.Logger})
	if err != nil {
		return nil, err
	}
	profileRegistries[key] = registry
	return registry, nil
}

// discoveryConfig builds the accountless discovery configuration of a job.
func discoveryConfig(config Config, root string, owner, machine domain.ID) (harness.DiscoveryConfig, error) {
	registry, err := nativeProfiles(config, machine)
	if err != nil {
		return harness.DiscoveryConfig{}, err
	}
	return harness.DiscoveryConfig{Root: root, OwnerID: owner, Logger: config.Logger, Profiles: registry}, nil
}
