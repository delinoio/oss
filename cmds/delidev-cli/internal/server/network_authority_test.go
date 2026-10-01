// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestNetworkCredentialAuthorityEdits(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*domain.ProxyDefinition)
		keep bool
	}{
		{"host", func(p *domain.ProxyDefinition) { p.Host = "other-proxy.example" }, false},
		{"port", func(p *domain.ProxyDefinition) { p.Port++ }, false},
		{"https-mode", func(p *domain.ProxyDefinition) { p.Mode = domain.ProxyHTTPS }, false},
		{"socks5-mode", func(p *domain.ProxyDefinition) { p.Mode = domain.ProxySOCKS5 }, false},
		{"direct", func(p *domain.ProxyDefinition) { *p = domain.ProxyDefinition{Name: p.Name, Mode: domain.ProxyDirect} }, false},
		{"rename", func(p *domain.ProxyDefinition) { p.Name = "Renamed" }, true},
		{"bypass", func(p *domain.ProxyDefinition) { p.Bypass = []domain.ProxyBypass{{Host: "local.example", Port: 443}} }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newIntegrationFixture(t)
			vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
			f.service.accountSecrets = vault
			client := delidevv1connect.NewNetworkServiceClient(f.httpServer.Client(), f.url)
			ctx := context.Background()
			paired := localOriginClient(t, &accountFixture{identity: f.service.Identity, endpoint: Endpoint{URL: f.url}})
			definition := domain.ProxyDefinition{Name: "Original", Mode: domain.ProxyHTTP, Host: "trusted-proxy.example", Port: 3128}
			save := func(actor security.Identity, mutation *pb.Mutation, secret string) *pb.SaveNetworkProfileResponse {
				t.Helper()
				raw, err := json.Marshal(definition)
				if err != nil {
					t.Fatal(err)
				}
				r, err := client.SaveNetworkProfile(ctx, ownerRequest(actor, &pb.SaveNetworkProfileRequest{Mutation: mutation, SchemaVersion: 1, DocumentJson: raw, CredentialJson: []byte(secret)}))
				if err != nil {
					t.Fatal(err)
				}
				return r.Msg
			}
			createID := domain.NewID()
			created := save(f.service.Identity, &pb.Mutation{RequestId: string(createID)}, outboundtest.Credential)
			selected, err := client.SelectNetworkProfile(ctx, ownerRequest(f.service.Identity, &pb.SelectNetworkProfileRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, ProfileId: created.Resource.Id, ProfileRevision: created.Resource.Revision}))
			if err != nil {
				t.Fatal(err)
			}
			test.edit(&definition)
			edit := &pb.Mutation{RequestId: string(domain.NewID()), Id: created.Resource.Id, ExpectedRevision: created.Resource.Revision}
			edited := save(paired, edit, "")
			var profile domain.NetworkProfile
			if domain.Decode(edited.Resource.DocumentJson, &profile) != nil || profile.Validate() != nil {
				t.Fatal("edited profile is invalid")
			}
			if test.keep && profile.CredentialGeneration != createID || !test.keep && profile.CredentialGeneration != "" {
				t.Fatal("credential retention did not follow authentication authority")
			}
			// Editing cannot rewrite an already selected snapshot or retire the
			// original generation needed by a request pinned before that edit.
			pinned, raw, err := f.service.outboundResolver()(ctx)
			if err != nil || pinned.Host != "trusted-proxy.example" || pinned.CredentialGeneration != createID || string(raw) != outboundtest.Credential {
				t.Fatal("edit changed the selected original authority", err)
			}
			clear(raw)
			replayed := save(paired, edit, "")
			if !replayed.Replayed || replayed.Resource.Revision != edited.Resource.Revision {
				t.Fatal("authority edit lost exact receipt replay")
			}
			_, err = client.SelectNetworkProfile(ctx, ownerRequest(paired, &pb.SelectNetworkProfileRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: selected.Msg.Resource.Id, ExpectedRevision: selected.Msg.Resource.Revision}, ProfileId: edited.Resource.Id, ProfileRevision: edited.Resource.Revision}))
			if err != nil {
				t.Fatal(err)
			}
			current, raw, err := f.service.outboundResolver()(ctx)
			if err != nil || current.CredentialGeneration != profile.CredentialGeneration || test.keep && string(raw) != outboundtest.Credential || !test.keep && len(raw) != 0 {
				t.Fatal("new selection acquired a credential from another authority", err)
			}
			clear(raw)
			if puts, deletes, retained := vault.counts(); puts != 1 || deletes != 0 || retained != 1 {
				t.Fatal("metadata edit rewrote or removed an immutable credential", puts, deletes, retained)
			}
			// Explicit re-entry binds a fresh immutable generation to the new
			// authority; it does not silently reuse the owner's old generation.
			if definition.Mode != domain.ProxyDirect {
				replacementID := domain.NewID()
				replacement := save(paired, &pb.Mutation{RequestId: string(replacementID), Id: edited.Resource.Id, ExpectedRevision: edited.Resource.Revision}, `{"username":"replacement-user","password":"replacement-password"}`)
				if domain.Decode(replacement.Resource.DocumentJson, &profile) != nil || profile.CredentialGeneration != replacementID {
					t.Fatal("explicit credential entry lost its new generation")
				}
			}
		})
	}
}

func TestNetworkAuthorityEditCannotSendOwnerCredentialToNewProxy(t *testing.T) {
	authorization := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization <- r.Header.Get("Proxy-Authorization")
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer proxy.Close()
	host, port, err := net.SplitHostPort(proxy.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	f := newIntegrationFixture(t)
	f.service.accountSecrets = &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	client := delidevv1connect.NewNetworkServiceClient(f.httpServer.Client(), f.url)
	paired := localOriginClient(t, &accountFixture{identity: f.service.Identity, endpoint: Endpoint{URL: f.url}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	definition := domain.ProxyDefinition{Name: "Trusted", Mode: domain.ProxyHTTP, Host: "trusted-proxy.example", Port: 3128}
	raw, _ := json.Marshal(definition)
	created, err := client.SaveNetworkProfile(ctx, ownerRequest(f.service.Identity, &pb.SaveNetworkProfileRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, SchemaVersion: 1, DocumentJson: raw, CredentialJson: []byte(outboundtest.Credential)}))
	if err != nil {
		t.Fatal(err)
	}
	definition.Host, definition.Port = host, uint16(n)
	raw, _ = json.Marshal(definition)
	edit := &pb.SaveNetworkProfileRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: created.Msg.Resource.Id, ExpectedRevision: created.Msg.Resource.Revision}, SchemaVersion: 1, DocumentJson: raw}
	edited, err := client.SaveNetworkProfile(ctx, ownerRequest(paired, edit))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.SelectNetworkProfile(ctx, ownerRequest(paired, &pb.SelectNetworkProfileRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, ProfileId: edited.Msg.Resource.Id, ProfileRevision: edited.Msg.Resource.Revision}))
	if err != nil {
		t.Fatal(err)
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = outbound.DirectDial
	transport := &outbound.Transport{Base: base, Resolve: f.service.outboundResolver()}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://fixture-origin.invalid/models", nil)
	if response, err := transport.RoundTrip(req); err == nil {
		response.Body.Close()
		t.Fatal("rejecting proxy unexpectedly completed a request")
	}
	select {
	case header := <-authorization:
		if header != "" {
			t.Fatal("paired client forwarded an existing owner credential to the changed proxy")
		}
	case <-ctx.Done():
		t.Fatal("controlled proxy did not receive the selected request")
	}
}
