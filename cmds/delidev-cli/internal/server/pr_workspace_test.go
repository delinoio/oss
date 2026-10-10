package server

import (
	"encoding/base64"
	"encoding/json"
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

func TestPRWorkspacePublicContentRejectsCredentialReflection(t *testing.T) {
	token := []byte("fixture-selected-private-pat")
	for _, reflected := range []string{string(token), base64.StdEncoding.EncodeToString(token), base64.RawURLEncoding.EncodeToString(token)} {
		if safePRWorkspaceObservation(map[string]string{"message": reflected}, token) == nil {
			t.Fatal("protected public content released")
		}
	}
	if safePRWorkspaceObservation(map[string]string{"message": "ordinary provider body"}, token) != nil {
		t.Fatal("ordinary content rejected")
	}
	if safePRWorkspaceObservation(json.RawMessage(`{"message":"\u0066ixture-selected-private-pat"}`), token) == nil {
		t.Fatal("escaped protected content released")
	}
}
