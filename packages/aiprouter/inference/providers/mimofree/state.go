// SPDX-License-Identifier: Apache-2.0
package mimofree

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

const stateID = "bootstrap-session-v1"
const stateTTL = time.Hour
const maxJWT = 64 << 10
const maxBootstrap = 128 << 10

type session struct {
	Client   string    `json:"client"`
	Affinity string    `json:"affinity"`
	JWT      string    `json:"jwt,omitempty"`
	Expires  time.Time `json:"expires"`
}

func freshSession() (session, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return session{}, err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	id := fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
	var affinity [16]byte
	if _, err := rand.Read(affinity[:]); err != nil {
		return session{}, err
	}
	return session{Client: id, Affinity: fmt.Sprintf("ses_%x", affinity)}, nil
}
func readSession(raw []byte) (session, error) {
	var s session
	if len(raw) > maxBootstrap || json.Unmarshal(raw, &s) != nil || len(s.Client) != 36 || !strings.HasPrefix(s.Affinity, "ses_") || len(s.Affinity) > 64 || !validToken(s.JWT, true) {
		return s, inference.ErrContinuation
	}
	return s, nil
}
func validToken(s string, empty bool) bool {
	if len(s) == 0 {
		return empty
	}
	if len(s) > maxJWT {
		return false
	}
	for _, c := range s {
		if c <= 32 || c >= 127 {
			return false
		}
	}
	return true
}

// Atomic initialization keeps client and affinity stable across replicas. Tokens never
// enter an adapter-global cache or filesystem; the supplied store owns encryption.
func loadSession(ctx context.Context, store inference.AdapterStateStore) (session, error) {
	updater, ok := store.(inference.AdapterStateUpdater)
	if !ok {
		return session{}, inference.ErrUnsupported
	}
	var result session
	err := updater.Update(ctx, stateID, stateTTL, func(raw []byte) ([]byte, error) {
		var err error
		if len(raw) == 0 {
			result, err = freshSession()
		} else {
			result, err = readSession(raw)
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	})
	return result, err
}
func saveToken(ctx context.Context, store inference.AdapterStateStore, s session, token string, expiry time.Time) (session, error) {
	updater, ok := store.(inference.AdapterStateUpdater)
	if !ok {
		return s, inference.ErrUnsupported
	}
	err := updater.Update(ctx, stateID, stateTTL, func(raw []byte) ([]byte, error) {
		current, err := readSession(raw)
		if err != nil {
			return nil, err
		}
		if current.Client != s.Client || current.Affinity != s.Affinity {
			return nil, inference.ErrContinuation
		}
		if token == "" && current.JWT != s.JWT {
			s = current
			return json.Marshal(current)
		}
		current.JWT = token
		current.Expires = expiry
		s = current
		return json.Marshal(current)
	})
	return s, err
}
func tokenExpiry(token string, now time.Time) time.Time {
	expiry := now.Add(50 * time.Minute)
	parts := strings.Split(token, ".")
	if len(parts) >= 2 {
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
		var p struct {
			Exp json.Number `json:"exp"`
		}
		if err == nil && json.Unmarshal(raw, &p) == nil {
			if seconds, err := p.Exp.Int64(); err == nil && seconds > 0 {
				expiry = time.Unix(seconds, 0)
			}
		}
	}
	// State is temporary even when upstream issues a longer-lived token.
	if expiry.After(now.Add(stateTTL)) {
		expiry = now.Add(stateTTL)
	}
	return expiry
}
func bootstrap(ctx context.Context, exec inference.AdapterExecutor, store inference.AdapterStateStore, s session) (session, error) {
	body, _ := json.Marshal(map[string]string{"client": s.Client})
	resp, err := exec.Do(ctx, inference.AdapterRequest{Method: http.MethodPost, URL: BootstrapURL, Body: body, AttemptTimeout: 15 * time.Second, Headers: http.Header{"Content-Type": {"application/json"}, "User-Agent": {userAgent}}})
	if err != nil {
		return s, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return s, invalid("mimo_bootstrap_failed")
	}
	if resp.ContentLength > maxBootstrap {
		return s, invalid("mimo_bootstrap_too_large")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBootstrap+1))
	if err != nil {
		return s, err
	}
	if len(raw) > maxBootstrap {
		return s, invalid("mimo_bootstrap_too_large")
	}
	var data struct {
		JWT string `json:"jwt"`
	}
	if !utf8.Valid(raw) || json.Unmarshal(raw, &data) != nil || !validToken(data.JWT, false) {
		return s, invalid("invalid_mimo_bootstrap")
	}
	return saveToken(ctx, store, s, data.JWT, tokenExpiry(data.JWT, time.Now()))
}
