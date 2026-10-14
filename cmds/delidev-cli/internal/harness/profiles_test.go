package harness

import (
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
)

var (
	testProfilesMu sync.Mutex
	testProfileSet = map[string]*nativeprofile.Registry{}
	testMachines   = map[*nativeprofile.Registry]domain.ID{}
)

// testProfiles returns the persistent profile registry of a test root. Repeated
// calls with one root share a registry, like one owner process would.
func testProfiles(t *testing.T, root string) *nativeprofile.Registry {
	t.Helper()
	testProfilesMu.Lock()
	defer testProfilesMu.Unlock()
	if registry := testProfileSet[root]; registry != nil {
		return registry
	}
	machine := domain.NewID()
	registry, err := nativeprofile.Open(nativeprofile.Config{Root: root, Owner: nativeprofile.OwnerWorker, Machine: machine})
	if err != nil {
		t.Fatal(err)
	}
	testProfileSet[root] = registry
	testMachines[registry] = machine
	return registry
}

// registryMachine returns the Worker machine identity of a test registry.
func registryMachine(t *testing.T, registry *nativeprofile.Registry) domain.ID {
	t.Helper()
	testProfilesMu.Lock()
	defer testProfilesMu.Unlock()
	return testMachines[registry]
}
