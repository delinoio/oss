package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
	"time"
)

func TestPRWorkspaceOpaqueReferencesFenceActorKindAndExpiry(t *testing.T) {
	s := &Service{}
	actor := domain.NewID()
	key := s.rememberPRReference(prWorkspaceReference{Actor: actor, Kind: prAvatarReference})
	if _, err := s.lookupPRReference(key, actor, prAvatarReference); err != nil {
		t.Fatal(err)
	}
	if _, err := s.lookupPRReference(key, domain.NewID(), prAvatarReference); err == nil {
		t.Fatal("foreign actor accepted")
	}
	if _, err := s.lookupPRReference(key, actor, prCommitReference); err == nil {
		t.Fatal("foreign reference family accepted")
	}
	ref := s.prWorkspaceRefs[key]
	ref.Expires = time.Now().Add(-time.Second)
	s.prWorkspaceRefs[key] = ref
	if _, err := s.lookupPRReference(key, actor, prAvatarReference); err == nil {
		t.Fatal("expired reference accepted")
	}
	for i := 0; i < 300; i++ {
		s.rememberPRReference(prWorkspaceReference{Actor: actor, Kind: prAvatarReference})
	}
	if len(s.prWorkspaceRefs) != 256 {
		t.Fatalf("reference capacity: %d", len(s.prWorkspaceRefs))
	}
}
