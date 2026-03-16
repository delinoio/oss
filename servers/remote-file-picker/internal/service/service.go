package service

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	remotefilepickerv1 "github.com/delinoio/oss/servers/remote-file-picker/gen/proto/remotefilepicker/v1"
)

// uploadRecord tracks the state of a single upload.
type uploadRecord struct {
	id            string
	fileName      string
	contentType   string
	contentLength int64
	provider      remotefilepickerv1.StorageProvider
	bucket        string
	objectKey     string
	signedURL     string
	publicURL     string
	status        remotefilepickerv1.UploadStatus
	createdAt     time.Time
	completedAt   time.Time
}

// Service implements the UploadService Connect RPC handler with in-memory upload tracking.
type Service struct {
	logger    *slog.Logger
	authToken string
	bucket    string
	provider  remotefilepickerv1.StorageProvider
	uploads   sync.Map // uploadID -> *uploadRecord
}

// New creates a new Service instance.
func New(logger *slog.Logger, authToken, bucket string, provider remotefilepickerv1.StorageProvider) *Service {
	return &Service{
		logger:    logger,
		authToken: authToken,
		bucket:    bucket,
		provider:  provider,
	}
}

// mockSignedURL generates a mock signed URL for a given bucket and object key.
func mockSignedURL(bucket, objectKey string) string {
	return fmt.Sprintf("https://%s.s3.amazonaws.com/%s?X-Amz-Signature=mock", bucket, objectKey)
}

// publicURL returns a public URL for a confirmed upload.
func publicURL(bucket, objectKey string) string {
	return fmt.Sprintf("https://%s.s3.amazonaws.com/%s", bucket, objectKey)
}
