package rpc

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestUploadOperationalErrorsDoNotLogBodiesURLsOrSecrets(t *testing.T) {
	const sensitive = "https://signed.example/put?X-Amz-Signature=secret screenshot-body"
	var logs bytes.Buffer
	service := &UploadService{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	ctx := WithCorrelationID(context.Background(), testCorrelationID)
	_ = service.mapError(ctx, "/devhud.v1.UploadService/CreateUpload", "create upload", errors.New(sensitive))
	if strings.Contains(logs.String(), sensitive) || strings.Contains(logs.String(), "X-Amz-Signature") || strings.Contains(logs.String(), "screenshot-body") {
		t.Fatalf("upload log exposed sensitive data: %s", logs.String())
	}
}
