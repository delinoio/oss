// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

func TestKiroIdentityCenterRegion(t *testing.T) {
	a, _ := newMock(t, responseStep{"POST", "https://oidc.eu-west-1.amazonaws.com/client/register", 200, `{"clientId":"id","clientSecret":"secret"}`, nil}, responseStep{"POST", "https://oidc.eu-west-1.amazonaws.com/device_authorization", 200, `{"deviceCode":"device","userCode":"ABCD","verificationUri":"https://device.sso.eu-west-1.amazonaws.com/"}`, jsonValue("startUrl", "https://company.awsapps.com/start")}, responseStep{"POST", "https://oidc.eu-west-1.amazonaws.com/token", 200, `{"accessToken":"access","refreshToken":"refresh","expiresIn":3600}`, nil}, responseStep{"POST", "https://oidc.eu-west-1.amazonaws.com/token", 200, `{"accessToken":"new","refreshToken":"rotated","expiresIn":3600}`, jsonValue("grantType", "refresh_token")})
	_, f, e := a.Start(context.Background(), testScope, "kiro", StartOptions{LoginProvider: "identity-center", SSORegion: "eu-west-1", APIRegion: "eu-central-1", StartURL: "https://company.awsapps.com/start"})
	if e != nil {
		t.Fatal(e)
	}
	due(a, &f)
	c, e := a.Poll(context.Background(), testScope, &f)
	if e != nil {
		t.Fatal(e)
	}
	c, e = a.Refresh(context.Background(), testScope, "kiro", c)
	if e != nil || c.Metadata["apiRegion"] != "eu-central-1" {
		t.Fatal(e)
	}
}
func TestCustomOrcaAndDevinPortals(t *testing.T) {
	t.Run("orca", func(t *testing.T) {
		a, _ := newMock(t, responseStep{"POST", "https://login.example/api/v1/auth/keys", 200, `{"key":"sk-orca-fixture","user_id":"user"}`, nil})
		ch, f, e := a.Start(context.Background(), testScope, "orcarouter-oauth", StartOptions{RedirectURI: "https://router.example/callback", BaseURL: "https://orca.example/v1", AuthBaseURL: "https://login.example"})
		if e != nil {
			t.Fatal(e)
		}
		u, _ := url.Parse(ch.URL)
		if u.Host != "login.example" {
			t.Fatal("wrong portal")
		}
		c, e := a.Complete(context.Background(), testScope, f, "code", f.State)
		if e != nil || c.APIBaseURL != "https://orca.example/v1" {
			t.Fatal(e)
		}
		if _, e = Resolve(Connection{Provider: c.Provider, Credential: &c, BaseURL: c.APIBaseURL}, "model", InboundChat); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("devin", func(t *testing.T) {
		a, _ := newMock(t, responseStep{"POST", "https://eu.windsurf.com/_route/register/exa.seat_management_pb.SeatManagementService/RegisterUser", 200, `{"api_key":"key","api_server_url":"https://eu.windsurf.com/_route/api_server"}`, func(t *testing.T, r *http.Request, b []byte) {
			if r.Header.Get("Connect-Protocol-Version") != "1" {
				t.Fatal("missing Connect header")
			}
		}})
		ch, f, e := a.Start(context.Background(), testScope, "devin", StartOptions{PortalURL: "https://eu.windsurf.com", RegisterURL: "https://eu.windsurf.com/_route/register", ClientID: "regional-client"})
		if e != nil {
			t.Fatal(e)
		}
		u, _ := url.Parse(ch.URL)
		if u.Query().Get("client_id") != "regional-client" {
			t.Fatal("wrong client")
		}
		if _, e = a.Complete(context.Background(), testScope, f, "ott$abcdefghijklmnopqrst", f.State); e != nil {
			t.Fatal(e)
		}
	})
}
