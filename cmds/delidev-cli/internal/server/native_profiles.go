package server

import (
	"log/slog"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
)

// nativeScope identifies the data root, account and persistent profile
// registry used by one server native launch. Server profiles stay local to the
// server's own storage root; credential rotation never changes a profile path.
type nativeScope struct {
	Root     string
	Account  domain.ID
	Profiles *nativeprofile.Registry
}

type serverProfileKey struct {
	root   string
	server domain.ID
}

var (
	serverProfilesMu sync.Mutex
	serverProfiles   = map[serverProfileKey]*nativeprofile.Registry{}
)

// nativeProfiles returns the server-local persistent profile registry.
func (s *Service) nativeProfiles() (*nativeprofile.Registry, error) {
	return serverNativeProfiles(s.Store.Root(), s.Identity.ServerID, s.logger)
}

func serverNativeProfiles(root string, serverID domain.ID, logger *slog.Logger) (*nativeprofile.Registry, error) {
	key := serverProfileKey{root: root, server: serverID}
	serverProfilesMu.Lock()
	defer serverProfilesMu.Unlock()
	if registry := serverProfiles[key]; registry != nil {
		return registry, nil
	}
	registry, err := nativeprofile.Open(nativeprofile.Config{Root: root, Owner: nativeprofile.OwnerServer, Server: serverID, Logger: logger})
	if err != nil {
		return nil, err
	}
	serverProfiles[key] = registry
	return registry, nil
}

// nativeScope binds one account to the server's registry.
func (s *Service) nativeScope(account domain.ID) (nativeScope, error) {
	registry, err := s.nativeProfiles()
	if err != nil {
		return nativeScope{}, subscriptionDenied()
	}
	return nativeScope{Root: s.Store.Root(), Account: account, Profiles: registry}, nil
}

// scopeFor returns a scope even when the registry cannot open; a scope without
// a registry makes every native launch fail closed instead of falling back.
func (s *Service) scopeFor(account domain.ID) nativeScope {
	scope, err := s.nativeScope(account)
	if err != nil {
		return nativeScope{Root: s.Store.Root(), Account: account}
	}
	return scope
}
