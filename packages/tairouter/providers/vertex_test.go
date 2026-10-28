// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestVertexServiceAccount(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	g := GoogleCredential{Type: GoogleServiceAccount, ClientEmail: "service@fixture.iam.gserviceaccount.com", ProjectID: "fixture", PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))}
	a, _ := newMock(t, responseStep{"POST", googleTokenURL, 200, `{"access_token":"google-token","expires_in":3600}`, func(t *testing.T, r *http.Request, b []byte) {
		v, _ := url.ParseQuery(string(b))
		parts := strings.Split(v.Get("assertion"), ".")
		if len(parts) != 3 {
			t.Fatal("missing signed assertion")
		}
		signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
		hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, hash[:], signature) != nil {
			t.Fatal("bad signature")
		}
		claims := jwtClaims(v.Get("assertion"))
		if claims["iss"] != g.ClientEmail || claims["aud"] != googleTokenURL || claims["scope"] != googleScope {
			t.Fatal("wrong grant claims")
		}
	}})
	c, e := a.ExchangeVertex(context.Background(), testScope, g, "")
	if e != nil {
		t.Fatal(e)
	}
	r, e := Resolve(Connection{Provider: "google-vertex", Credential: &c, Auth: OAuth}, "gemini", InboundChat)
	if e != nil || r.Headers.Get("Authorization") != "Bearer google-token" || r.Headers.Get("x-goog-api-key") != "" {
		t.Fatalf("incorrect Vertex bearer route: %v", e)
	}
}
func TestVertexExternalAndImpersonation(t *testing.T) {
	g := GoogleCredential{Type: GoogleExternalAccount, Audience: "//iam.googleapis.com/projects/1/locations/global/workloadIdentityPools/pool/providers/provider", SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt", ImpersonationURL: "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/account@project.iam.gserviceaccount.com:generateAccessToken", Source: &GoogleSubjectSource{URL: "https://identity.example/token", Headers: map[string]string{"X-Identity": "personal"}}}
	g.Source.Format.Type = "json"
	g.Source.Format.Field = "token"
	a, _ := newMock(t, responseStep{"GET", "https://identity.example/token", 200, `{"token":"subject"}`, nil}, responseStep{"POST", "https://sts.googleapis.com/v1/token", 200, `{"access_token":"federated","expires_in":3600}`, formValue("subject_token", "subject")}, responseStep{"POST", g.ImpersonationURL, 200, `{"accessToken":"impersonated","expireTime":"2023-11-14T23:13:20Z"}`, func(t *testing.T, r *http.Request, b []byte) {
		if r.Header.Get("Authorization") != "Bearer federated" {
			t.Fatal("wrong impersonation bearer")
		}
	}})
	c, e := a.ExchangeVertex(context.Background(), testScope, g, "")
	if e != nil || c.AccessToken != "impersonated" || c.Google == nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(c)
	if strings.Contains(string(b), `"subject"`) {
		t.Fatal("one-time subject retained")
	}
}
func TestVertexAuthorizedUserRefresh(t *testing.T) {
	g := GoogleCredential{Type: GoogleAuthorizedUser, ClientID: "id", ClientSecret: "secret", RefreshToken: "old"}
	a, _ := newMock(t, responseStep{"POST", googleTokenURL, 200, `{"access_token":"access","refresh_token":"rotated","expires_in":3600}`, formValue("refresh_token", "old")}, responseStep{"POST", googleTokenURL, 200, `{"access_token":"fresh","expires_in":3600}`, formValue("refresh_token", "rotated")})
	c, e := a.ExchangeVertex(context.Background(), testScope, g, "")
	if e != nil {
		t.Fatal(e)
	}
	c, e = a.Refresh(context.Background(), testScope, "google-vertex", c)
	if e != nil || c.Google.RefreshToken != "rotated" {
		t.Fatal(e)
	}
}
func TestVertexRefusesAmbientAndUntrustedEndpoints(t *testing.T) {
	for _, src := range []*GoogleSubjectSource{{File: "/operator/home/adc"}, {Executable: json.RawMessage(`{"command":"steal"}`)}, {URL: "http://metadata.google.internal/token"}, {EnvironmentID: "aws1"}} {
		g := GoogleCredential{Type: GoogleExternalAccount, Audience: "//iam.googleapis.com/projects/test", SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt", Source: src}
		a, _ := newMock(t)
		if _, e := a.ExchangeVertex(context.Background(), testScope, g, ""); !errors.Is(e, ErrConnection) {
			t.Fatalf("ambient source accepted: %v", e)
		}
	}
	g := GoogleCredential{Type: GoogleAuthorizedUser, ClientID: "id", ClientSecret: "secret", RefreshToken: "refresh", TokenURI: "https://attacker.example/token"}
	a, _ := newMock(t)
	if _, e := a.ExchangeVertex(context.Background(), testScope, g, ""); !errors.Is(e, ErrConnection) {
		t.Fatal(e)
	}
}
