package rpc

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
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

func TestRemovingUploadProtocolStateUsesFinalization(t *testing.T) {
	pendingRemoval := domain.Upload{State: domain.UploadStateRemoving}
	if got := protocolUploadState(pendingRemoval); got != devhudv1.UploadState_UPLOAD_STATE_PENDING {
		t.Fatalf("pending removal state = %v", got)
	}
	finalizedAt := time.Now()
	finalizedRemoval := domain.Upload{State: domain.UploadStateRemoving, FinalizedAt: &finalizedAt}
	if got := protocolUploadState(finalizedRemoval); got != devhudv1.UploadState_UPLOAD_STATE_FINALIZED {
		t.Fatalf("finalized removal state = %v", got)
	}
}

func TestUploadStateFiltersLeaveRemovalClassificationToStorage(t *testing.T) {
	pending, err := uploadStates([]devhudv1.UploadState{devhudv1.UploadState_UPLOAD_STATE_PENDING})
	if err != nil || len(pending) != 2 || pending[0] != domain.UploadStatePending || pending[1] != domain.UploadStatePublishing {
		t.Fatalf("pending filter = %v, err=%v", pending, err)
	}
	finalized, err := uploadStates([]devhudv1.UploadState{devhudv1.UploadState_UPLOAD_STATE_FINALIZED})
	if err != nil || len(finalized) != 1 || finalized[0] != domain.UploadStateFinalized {
		t.Fatalf("finalized filter = %v, err=%v", finalized, err)
	}
}

func TestUploadStateFiltersAreNormalizedForPaginationScope(t *testing.T) {
	states, err := uploadStates([]devhudv1.UploadState{
		devhudv1.UploadState_UPLOAD_STATE_FINALIZED,
		devhudv1.UploadState_UPLOAD_STATE_PENDING,
		devhudv1.UploadState_UPLOAD_STATE_FINALIZED,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.UploadState{
		domain.UploadStatePending,
		domain.UploadStatePublishing,
		domain.UploadStateFinalized,
	}
	if len(states) != len(want) {
		t.Fatalf("normalized states = %v", states)
	}
	for index := range want {
		if states[index] != want[index] {
			t.Fatalf("normalized states = %v", states)
		}
	}
}
