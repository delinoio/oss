// Package github contains RealQA's sole internal tracker adapter boundary.
package github

import (
	"errors"
	"net/url"
	"strings"
)

type Authorization struct {
	clientID string
}

func NewAuthorization(clientID string) (*Authorization, error) {
	if clientID == "" || len(clientID) > 255 ||
		strings.TrimSpace(clientID) != clientID ||
		strings.ContainsAny(clientID, " \t\r\n:/") {
		return nil, errors.New("realqa github: client ID is invalid")
	}
	return &Authorization{clientID: clientID}, nil
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
