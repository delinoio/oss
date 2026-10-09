// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"google.golang.org/protobuf/encoding/protojson"
)

// This test-only process supplies retained original native metadata to the
// mobile HTTPS integration suite. It never starts a Worker or native harness.
// Its private fixture file is owned by the invoking temporary test directory;
// neither ordinary tests nor the product binary expose this boundary.
func TestMobileHTTPSFixtureProcess(t *testing.T) {
	path := os.Getenv("DELIDEV_MOBILE_FIXTURE_READY")
	if path == "" {
		t.Skip("activated only by the isolated mobile HTTPS suite")
	}
	question, id := questionResponseFixture(t)
	steer, request := newSteerFixture(t)
	for _, fixture := range []*publicationFixture{question, steer} {
		token, err := security.RandomToken()
		if err != nil {
			t.Fatal("could not create fixture owner identity")
		}
		fixture.service.Identity.Token = token
	}
	expired := domain.NewID()
	code := strings.Repeat("x", 43)
	hash := sha256.Sum256([]byte(code))
	_, err := question.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.expired-mobile-pairing", nil, func(tx *store.Tx) (any, error) {
		if _, err := tx.Put(domain.PairingKind, expired, 0, "", "", domain.Pairing{Name: "Expired fixture", Type: domain.ClientDevice, ExpiresAt: time.Now().UTC().Add(-time.Minute)}); err != nil {
			return nil, err
		}
		return nil, tx.PutPairingVerifier(expired, hash[:])
	})
	if err != nil {
		t.Fatal("could not seed expired fixture")
	}
	raw, err := protojson.Marshal(request)
	if err != nil {
		t.Fatal("could not encode original fixture")
	}
	info := map[string]any{
		"question": map[string]any{"http_origin": question.http.URL, "server_id": question.service.Identity.ServerID, "token": question.service.Identity.Token, "session_id": question.input.SessionID, "interaction_id": id, "expired_pairing_id": expired, "expired_code": code},
		"steer":    map[string]any{"http_origin": steer.http.URL, "server_id": steer.service.Identity.ServerID, "token": steer.service.Identity.Token, "session_id": steer.input.SessionID, "request": json.RawMessage(raw)},
	}
	body, err := json.Marshal(info)
	if err != nil {
		t.Fatal("could not serialize fixture readiness")
	}
	if err = os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal("could not retain private fixture readiness")
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}
