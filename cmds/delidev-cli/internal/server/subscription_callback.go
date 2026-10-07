// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

var subscriptionBrowserState = regexp.MustCompile(`^[a-zA-Z0-9_-]{16,512}$`)

func serverLoginCallback(authorization string) (string, string, bool) {
	if len(authorization) > 8192 {
		return "", "", false
	}
	u, err := url.Parse(authorization)
	if err != nil || u.Scheme != "https" || u.Host != "auth.openai.com" || u.Path != "/oauth/authorize" || u.User != nil || u.Fragment != "" {
		return "", "", false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", "", false
	}
	for _, values := range q {
		if len(values) != 1 {
			return "", "", false
		}
	}
	state := q.Get("state")
	callback := q.Get("redirect_uri")
	if !subscriptionBrowserState.MatchString(state) {
		return "", "", false
	}
	// Preserve the registered authority returned by the original native login.
	// Codex 0.151.0 uses localhost; 0.159.2 uses the literal IPv4 loopback.
	switch callback {
	case "http://localhost:1457/auth/callback", "http://127.0.0.1:1457/auth/callback":
	default:
		return "", "", false
	}
	return callback, state, true
}
func validServerLoginProgress(authorization, code string) bool {
	if code != "" {
		u, err := url.Parse(authorization)
		return err == nil && len(authorization) <= 8192 && u.Scheme == "https" && (u.Host == "auth.openai.com" || u.Host == "chatgpt.com") && u.User == nil && u.Fragment == "" && subscriptionUserCode.MatchString(code)
	}
	_, _, ok := serverLoginCallback(authorization)
	return ok
}
func subscriptionCallbackQuery(raw []byte, state string) (string, bool) {
	if len(raw) < 1 || len(raw) > 16<<10 || !utf8.Valid(raw) {
		return "", false
	}
	q, err := url.ParseQuery(string(raw))
	if err != nil {
		return "", false
	}
	for key, values := range q {
		if key != "code" && key != "state" && key != "scope" {
			return "", false
		}
		if len(values) != 1 || len(values[0]) > 8192 || strings.IndexFunc(values[0], unicode.IsControl) >= 0 {
			return "", false
		}
	}
	if q.Get("state") != state || q.Get("code") == "" {
		return "", false
	}
	return q.Encode(), true
}

// Native token exchange stays in the original installed Codex process. This
// endpoint is a single-use relay to its exact loopback callback, never a proxy.
func (s *Service) ForwardSubscriptionCallback(ctx context.Context, req *connect.Request[pb.ForwardSubscriptionCallbackRequest]) (*connect.Response[pb.ForwardSubscriptionCallbackResponse], error) {
	defer clear(req.Msg.CallbackQuery)
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	account, operation := domain.ID(req.Msg.AccountId), domain.ID(req.Msg.OperationId)
	if account.Validate() != nil || operation.Validate() != nil || len(req.Msg.CallbackQuery) > 16<<10 {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	var destination string
	var original domain.ServerSubscriptionOperation
	var grok bool
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, a, err := subscriptionAccount(tx, account, 0)
		if err != nil {
			return err
		}
		if a.Subscription == nil || a.Subscription.RecoveryRequired || a.Subscription.ServerOperation == nil || a.Subscription.Pending == nil {
			return subscriptionDenied()
		}
		o := a.Subscription.ServerOperation
		if o.ID != operation || o.Actor != actor || o.Epoch != s.subscriptionServerEpoch() || o.State != domain.SubscriptionWaiting || !o.NativeStarted || o.CallbackForwarded || !time.Now().Before(o.ExpiresAt) || a.Subscription.Pending.ID != o.ID || a.Subscription.Pending.Canceled || subscriptionActorValid(tx, o.Actor) != nil {
			return subscriptionDenied()
		}
		p := s.subscriptionProgress[o.ID]
		callback, state, ok := serverLoginCallback(p.URL)
		grok = a.SubscriptionService == domain.SubscriptionGrok
		if grok {
			callback, state, ok = grokServerCallback(p.URL)
		}
		if !ok || p.UserCode != "" {
			return subscriptionDenied()
		}
		query, ok := subscriptionCallbackQuery(req.Msg.CallbackQuery, state)
		if grok {
			query, ok = grokCallbackQuery(req.Msg.CallbackQuery, state)
		}
		if !ok {
			return subscriptionDenied()
		}
		destination = callback + "?" + query
		original = *o
		return nil
	})
	if err == nil {
		// Synchronize dispatch ownership before HTTP. A lost response, duplicate or
		// restart can inspect progress but can never repeat this code delivery.
		_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.server.callback", struct{ Account, Operation domain.ID }{account, operation}, func(tx *store.Tx) (any, error) {
			_, a, err := subscriptionAccount(tx, account, 0)
			if err != nil {
				return nil, err
			}
			o := a.Subscription.ServerOperation
			if o == nil || o.ID != original.ID || o.CallbackForwarded || o.Epoch != original.Epoch || o.State != domain.SubscriptionWaiting || a.Subscription.RecoveryRequired || a.Subscription.Pending == nil || a.Subscription.Pending.Canceled || subscriptionActorValid(tx, actor) != nil {
				return nil, subscriptionDenied()
			}
			r, _, err := subscriptionAccount(tx, account, 0)
			if err != nil {
				return nil, err
			}
			o.CallbackForwarded = true
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
			return struct{}{}, nil
		})
	}
	unlock()
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, DisableKeepAlives: true, ResponseHeaderTimeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	var callbackTransport http.RoundTripper = transport
	if s.subscriptionCallbackTransport != nil {
		callbackTransport = s.subscriptionCallbackTransport
	}
	client := &http.Client{Transport: callbackTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(bounded, http.MethodGet, destination, nil)
	destination = ""
	if err == nil {
		// Resolve no remote hostname and inherit no proxy. The original native
		// callback is IPv4-only; retain its exact registered HTTP authority.
		request.Host = request.URL.Host
		if !grok {
			request.URL.Host = "127.0.0.1:1457"
		}
		response, e := client.Do(request)
		err = e
		if response != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusFound {
				err = subscriptionDenied()
			}
		}
	}
	if err != nil {
		s.logger.WarnContext(ctx, "server_subscription_callback_unconfirmed", "operation_id", operation)
		return nil, rpc.Error(domain.Fail(domain.RecoveryRequired, "Callback delivery was not confirmed.", "Inspect the original login. The callback is never sent again."), c)
	}
	s.logger.InfoContext(ctx, "server_subscription_callback_forwarded", "operation_id", operation)
	return connect.NewResponse(&pb.ForwardSubscriptionCallbackResponse{Accepted: true}), nil
}
