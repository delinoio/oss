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
	if s.creation != nil && nativeID(s.creation.identity.id, "ses") {
		base := "/session/" + s.creation.identity.id
		valid = valid || method == http.MethodGet && path == base
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
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(bounded, method, s.origin+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, sessionInvalid()
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("x-opencode-directory", s.cwd)
	request.SetBasicAuth("delidev", s.password)
	if method == http.MethodPost {
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
	return raw, response.StatusCode, nil
}
