package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/delinoio/oss/servers/devhud-realqa/internal/service"
)

const maxGitHubWebhookBytes = 1024 * 1024

func issueDeletionWebhook(
	submissions *service.Submission,
	secret []byte,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if submissions == nil || len(secret) < 32 ||
			request.Method != http.MethodPost ||
			request.Header.Get("X-GitHub-Event") != "issues" ||
			request.Header.Get("Content-Type") != "application/json" {
			http.Error(writer, "webhook rejected", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(io.LimitReader(
			request.Body, maxGitHubWebhookBytes+1))
		if err != nil || len(body) > maxGitHubWebhookBytes ||
			!validGitHubSignature(
				request.Header.Get("X-Hub-Signature-256"), secret, body) {
			http.Error(writer, "webhook rejected", http.StatusForbidden)
			return
		}
		var payload struct {
			Action string `json:"action"`
			Issue  struct {
				ID json.Number `json:"id"`
			} `json:"issue"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.UseNumber()
		if err = decoder.Decode(&payload); err != nil ||
			payload.Action == "" || payload.Issue.ID == "" {
			http.Error(writer, "webhook rejected", http.StatusBadRequest)
			return
		}
		if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			http.Error(writer, "webhook rejected", http.StatusBadRequest)
			return
		}
		if payload.Action != "deleted" {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		issueID := payload.Issue.ID.String()
		if _, err = strconv.ParseInt(issueID, 10, 64); err != nil {
			http.Error(writer, "webhook rejected", http.StatusBadRequest)
			return
		}
		if err = submissions.DeleteIssueAssets(
			request.Context(), issueID); err != nil {
			http.Error(writer, "webhook unavailable", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusAccepted)
	})
}

func validGitHubSignature(value string, secret, body []byte) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	return hmac.Equal(provided, mac.Sum(nil))
}
