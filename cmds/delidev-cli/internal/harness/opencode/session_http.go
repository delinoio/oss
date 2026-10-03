package opencode

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

func (s *sessionAPI) request(ctx context.Context, method, path string, body []byte, expected int) ([]byte, int, error) {
	// The unexported owner supplies the same fixed-authority HTTP transport as
	// discovery. Its route set is separate: no arbitrary URL/query, config write,
	// login, provider switch, fork, deletion or prompt retry is available here.
	valid := method == http.MethodPost && path == "/session" && s.creation != nil
	valid = valid || s.runtimeRead && s.creation == nil && method == http.MethodGet && (path == "/config" || path == "/provider" || path == "/path" || path == "/agent") && len(body) == 0
	valid = valid || s.reconciliationRead && method == http.MethodGet && len(body) == 0 && (path == "/config" || path == "/provider" || path == "/path" || path == "/agent")
	valid = valid || s.projectRead && method == http.MethodGet && len(body) == 0 && (path == "/project/current" || path == "/session?limit=2")
	valid = valid || method == http.MethodGet && s.historyRead != nil && path == s.historyRead.path && len(body) == 0
	valid = valid || s.checkpointRead && method == http.MethodGet && len(body) == 0 && (path == "/session?limit=2" || path == "/permission" || path == "/question" || path == "/session/status")
	if s.creationLookup && s.creation != nil && s.creation.attempted && s.input == nil && method == http.MethodGet && len(body) == 0 {
		valid = valid || path == "/session?limit=2"
		if nativeID(s.creationCandidate, "ses") {
			base := "/session/" + s.creationCandidate
			valid = valid || path == base || path == base+"/message?limit=1" || path == "/session/status"
		}
	}
	if s.compactionAttempt != nil {
		valid = valid || s.compactionAttempt.attempted && method == http.MethodPost && path == s.compactionAttempt.path && mutationDigest(body) == s.compactionAttempt.digest
	}
	if s.replyAttempt != nil {
		valid = valid || method == http.MethodPost && path == s.replyAttempt.path && mutationDigest(body) == s.replyAttempt.digest
	}
	if s.permissionRestore != nil {
		valid = valid || method == http.MethodPatch && path == s.permissionRestore.path && mutationDigest(body) == s.permissionRestore.digest
	}
	if s.observer != nil {
		valid = valid || method == http.MethodGet && (path == "/permission" || path == "/question" || path == "/session/status")
	}
	if s.creation != nil && nativeID(s.creation.identity.id, "ses") {
		base := "/session/" + s.creation.identity.id
		valid = valid || s.abortAttempt && method == http.MethodPost && path == base+"/abort" && len(body) == 0
		valid = valid || method == http.MethodGet && path == base
		valid = valid || s.todoRead && method == http.MethodGet && path == base+"/todo" && len(body) == 0
		if s.input != nil {
			valid = valid || method == http.MethodPost && path == base+"/prompt_async" || method == http.MethodGet && path == base+"/message/"+s.input.receipt.MessageID
		}
	}
	if !valid || len(body) > maxHTTPBody || s.password == "" || s.client == nil || s.alive == nil || strings.ContainsAny(s.cwd, "\r\n\x00") {
		return nil, 0, sessionInvalid()
	}
	if err := s.alive(); err != nil {
		return nil, 0, launchError(err)
	}
	duration := 10 * time.Second
	if s.compactionAttempt != nil && method == http.MethodPost && path == s.compactionAttempt.path {
		duration = 15 * time.Minute
	}
	bounded, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	if (method == http.MethodPost || method == http.MethodPatch) && s.events != nil {
		if problem := s.events.status(); problem != nil {
			return nil, 0, problem
		}
		// A lost native event subscription invalidates an in-flight execution
		// attempt too. Cancellation retains the original durable mutation claim;
		// it never asserts that the native server did not accept the request.
		stop := context.AfterFunc(s.events.ctx, cancel)
		defer stop()
	}
	request, err := http.NewRequestWithContext(bounded, method, s.origin+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, sessionInvalid()
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("x-opencode-directory", s.cwd)
	request.SetBasicAuth("delidev", s.password)
	if method == http.MethodPost || method == http.MethodPatch {
		request.Header.Set("Content-Type", "application/json")
		// Prevent automatic transport replay, including for a body that net/http
		// could otherwise reconstruct. The transport also disables keep-alives.
		request.GetBody = nil
	}
	response, err := s.client.Do(request)
	if err != nil {
		return nil, 0, unavailable()
	}
	defer response.Body.Close()
	if len(response.Header.Values("Content-Encoding")) != 0 || response.ContentLength > maxHTTPBody {
		return nil, response.StatusCode, sessionProblem()
	}
	missingInput := method == http.MethodGet && s.input != nil && path == "/session/"+s.input.receipt.SessionID+"/message/"+s.input.receipt.MessageID && response.StatusCode == http.StatusNotFound
	if response.StatusCode != expected && !missingInput {
		return nil, response.StatusCode, sessionProblem()
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxHTTPBody+1))
	if err != nil || len(raw) > maxHTTPBody {
		return nil, response.StatusCode, sessionProblem()
	}
	if err := s.accountReconciliationRead(raw); err != nil {
		return nil, response.StatusCode, err
	}
	if response.StatusCode == http.StatusNoContent {
		if len(raw) != 0 || len(response.Header.Values("Content-Type")) != 0 {
			return nil, response.StatusCode, sessionProblem()
		}
	} else {
		values := response.Header.Values("Content-Type")
		if len(values) != 1 {
			return nil, response.StatusCode, sessionProblem()
		}
		media, params, err := mime.ParseMediaType(values[0])
		if err != nil || media != "application/json" || len(params) != 0 {
			return nil, response.StatusCode, sessionProblem()
		}
	}
	if err := s.alive(); err != nil {
		return nil, response.StatusCode, launchError(err)
	}
	if missingInput {
		// Diagnostic bodies may contain local paths, credentials or input. They
		// never become observations and are not required to infer non-rejection.
		return nil, response.StatusCode, nil
	}
	if method == http.MethodGet && s.historyRead != nil && path == s.historyRead.path {
		values := response.Header.Values("X-Next-Cursor")
		if len(values) > 1 || len(values) == 1 && !validHistoryCursor(values[0]) || len(values) == 0 && len(response.Header.Values("Link")) != 0 {
			return nil, response.StatusCode, sessionProblem()
		}
		if len(values) == 1 {
			s.historyRead.cursor = values[0]
		}
	}
	return raw, response.StatusCode, nil
}

// Called under the session gate. Count actual bodies across all repeated reads,
// including configuration, predecessor pages and canonicalized-away whitespace.
func (s *sessionAPI) accountReconciliationRead(raw []byte) error {
	if s.reconciliation == ReconciliationReading {
		if len(raw) > maxObservedBytes-s.reconciliationReadBytes {
			return eventBound()
		}
		s.reconciliationReadBytes += len(raw)
	}
	return nil
}
