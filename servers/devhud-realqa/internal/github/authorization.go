// Package github contains RealQA's sole internal tracker adapter boundary.
package github

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Authorization struct {
	clientID string
	appSlug  string
	state    *StateCodec
	now      func() time.Time
}

func NewAppAuthorization(
	clientID string,
	appSlug string,
	state *StateCodec,
	now func() time.Time,
) (*Authorization, error) {
	authorization, err := NewSignedAuthorization(clientID, state, now)
	if err != nil {
		return nil, err
	}
	if !validAppSlug(appSlug) {
		return nil, errors.New("realqa github: app slug is invalid")
	}
	authorization.appSlug = appSlug
	return authorization, nil
}

func NewAuthorization(clientID string) (*Authorization, error) {
	if clientID == "" || len(clientID) > 255 ||
		strings.TrimSpace(clientID) != clientID ||
		strings.ContainsAny(clientID, " \t\r\n:/") {
		return nil, errors.New("realqa github: client ID is invalid")
	}
	return &Authorization{clientID: clientID}, nil
}

func NewSignedAuthorization(
	clientID string,
	state *StateCodec,
	now func() time.Time,
) (*Authorization, error) {
	authorization, err := NewAuthorization(clientID)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, errors.New("realqa github: callback state signer is required")
	}
	if now == nil {
		now = time.Now
	}
	authorization.state, authorization.now = state, now
	return authorization, nil
}

func (authorization *Authorization) OAuthState(
	ownerKind string,
	ownerID uuid.UUID,
) (string, error) {
	if authorization == nil || authorization.state == nil || authorization.now == nil {
		return "", errors.New("realqa github: signed OAuth state is unavailable")
	}
	return authorization.state.Issue(
		Owner{Kind: OwnerKind(ownerKind), ID: ownerID},
		CallbackPurposeOAuth,
		authorization.now(),
	)
}

func (authorization *Authorization) ConnectionTarget(
	ownerKind string,
	ownerID uuid.UUID,
) (string, string, error) {
	if authorization == nil || !validAppSlug(authorization.appSlug) ||
		authorization.state == nil || authorization.now == nil {
		return "", "", errors.New("realqa github: app installation target is unavailable")
	}
	state, err := authorization.state.Issue(
		Owner{Kind: OwnerKind(ownerKind), ID: ownerID},
		CallbackPurposeApp,
		authorization.now(),
	)
	if err != nil {
		return "", "", err
	}
	target := &url.URL{
		Scheme: "https", Host: "github.com",
		Path: "/apps/" + authorization.appSlug + "/installations/new",
	}
	query := target.Query()
	query.Set("state", state)
	target.RawQuery = query.Encode()
	return target.String(), state, nil
}

// Target constructs only the exact GitHub.com OAuth authorization target.
func (authorization *Authorization) Target(state string) (string, error) {
	if authorization == nil || authorization.clientID == "" ||
		len(state) < 32 || strings.ContainsAny(state, " \t\r\n") {
		return "", errors.New("realqa github: authorization state is invalid")
	}
	target := &url.URL{
		Scheme: "https", Host: "github.com", Path: "/login/oauth/authorize",
	}
	query := target.Query()
	query.Set("client_id", authorization.clientID)
	query.Set("state", state)
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func validAppSlug(value string) bool {
	if value == "" || len(value) > 100 || value[0] == '-' ||
		value[len(value)-1] == '-' {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}
