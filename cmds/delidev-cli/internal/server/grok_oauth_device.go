// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

const grokVerificationURI = "https://accounts.x.ai/oauth2/device"

type grokDeviceGrant struct {
	device   []byte
	user     string
	expires  time.Time
	interval time.Duration
}

func (c grokOAuthHTTP) device(ctx context.Context) (grant grokDeviceGrant, returned error) {
	defer func() {
		if returned != nil {
			clear(grant.device)
		}
	}()
	status, fields, err := c.request(ctx, http.MethodPost, grokDeviceEndpoint, map[string][]byte{"client_id": []byte(subscription.GrokClientID), "scope": []byte(grokScopes)}, nil)
	if err != nil {
		return grant, err
	}
	defer clearOAuthObject(fields)
	var uri string
	seconds, err := strconv.ParseInt(string(fields["expires_in"]), 10, 32)
	if err != nil {
		return grant, grokOAuthProblem()
	}
	interval := int64(5)
	if raw, present := fields["interval"]; present {
		interval, err = strconv.ParseInt(string(raw), 10, 32)
	}
	if status != http.StatusOK || err != nil || seconds < 1 || seconds > 3600 || interval < 1 || interval > 60 || json.Unmarshal(fields["verification_uri"], &uri) != nil || uri != grokVerificationURI || json.Unmarshal(fields["user_code"], &grant.user) != nil || !subscriptionUserCode.MatchString(grant.user) {
		return grant, grokOAuthProblem()
	}
	grant.device, err = decodeOAuthASCIIKey(fields["device_code"])
	if err != nil {
		return grant, grokOAuthProblem()
	}
	// The complete URI embeds a code. DeliDev deliberately publishes only the
	// pinned page and its separate memory-only user-code presentation.
	grant.interval = time.Duration(interval) * time.Second
	grant.expires = time.Now().UTC().Add(time.Duration(seconds) * time.Second)
	return grant, nil
}

func (c grokOAuthHTTP) poll(ctx context.Context, device []byte) (grokTokenResult, oauthDevicePoll, error) {
	status, fields, err := c.request(ctx, http.MethodPost, grokTokenEndpoint, map[string][]byte{"grant_type": []byte("urn:ietf:params:oauth:grant-type:device_code"), "client_id": []byte(subscription.GrokClientID), "device_code": device}, nil)
	if err != nil {
		return grokTokenResult{}, oauthDeviceIssued, err
	}
	defer clearOAuthObject(fields)
	if status == http.StatusBadRequest {
		var code string
		if json.Unmarshal(fields["error"], &code) == nil {
			switch code {
			case "authorization_pending":
				return grokTokenResult{}, oauthDevicePending, nil
			case "slow_down":
				return grokTokenResult{}, oauthDeviceSlowDown, nil
			case "access_denied":
				return grokTokenResult{}, oauthDeviceIssued, domain.Fail(domain.PermissionDenied, "Grok sign-in was declined.", "Start a new login only after this operation is settled.")
			case "expired_token":
				return grokTokenResult{}, oauthDeviceIssued, domain.Fail(domain.CursorExpired, "Grok sign-in expired.", "Start a new login after this operation is settled.")
			}
		}
	}
	result, err := c.parseToken(ctx, status, fields, "", "")
	return result, oauthDeviceIssued, err
}

func (c grokOAuthHTTP) userIdentity(ctx context.Context, access []byte) (subscription.Identity, error) {
	var identity subscription.Identity
	status, fields, err := c.request(ctx, http.MethodGet, grokUserInfo, nil, access)
	if err != nil {
		return identity, err
	}
	defer clearOAuthObject(fields)
	var user, email, name string
	if status != http.StatusOK || json.Unmarshal(fields["sub"], &user) != nil || len(user) == 0 || len(user) > 256 || domain.Text(user, "verified identity", 256, true) != nil {
		return identity, grokOAuthProblem()
	}
	for key, target := range map[string]*string{"email": &email, "name": &name} {
		if raw := fields[key]; len(raw) != 0 && string(raw) != "null" && (json.Unmarshal(raw, target) != nil || domain.Text(*target, "verified identity", 1024, false) != nil) {
			return identity, grokOAuthProblem()
		}
	}
	// The current fixed personal profile cannot reinterpret a team or
	// organization grant as the user's personal principal during refresh.
	var principalType, principalID string
	for key, target := range map[string]*string{"principal_type": &principalType, "principal_id": &principalID} {
		if raw := fields[key]; len(raw) != 0 && string(raw) != "null" && json.Unmarshal(raw, target) != nil {
			return identity, grokOAuthProblem()
		}
	}
	if principalType != "" || principalID != "" {
		if principalType != "User" || principalID != user {
			return identity, grokOAuthProblem()
		}
	}
	identity = subscription.Identity{Service: domain.SubscriptionGrok, Issuer: subscription.GrokIssuer, Account: user, User: user, PrincipalType: "User", PrincipalID: user, Email: email, DisplayName: name}
	return identity, nil
}
