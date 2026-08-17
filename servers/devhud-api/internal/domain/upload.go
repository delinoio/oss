package domain

import (
	"context"
	"errors"
	"time"
)

const (
	UploadMaximumObjectBytes       uint64 = 50 * 1024 * 1024
	UploadMaximumSubmissionImages  uint64 = 10
	UploadMaximumRollingDayBytes   uint64 = 1024 * 1024 * 1024
	UploadMaximumStoredBytes       uint64 = 20 * 1024 * 1024 * 1024
	UploadMaximumURLsPerHour       uint64 = 120
	UploadSignedURLLifetime               = 15 * time.Minute
	UploadStagingLifetime                 = 24 * time.Hour
	UploadStagingCleanupRetryDelay        = time.Minute
	UploadOperationLease                  = 2 * time.Minute
	UploadMaximumPageSize          uint32 = 100
	UploadDefaultPageSize          uint32 = 50
	UploadMaximumPageTokenBytes           = 2048
	UploadMaximumRasterAxis        uint32 = 4096
	UploadMaximumRasterPixels      uint64 = 16_777_216
)

type UploadState int16

const (
	UploadStatePending UploadState = iota + 1
	UploadStatePublishing
	UploadStateFinalized
	UploadStateRemoving
	UploadStateQuarantined
	UploadStateDeleted
	UploadStateExpired
	UploadStateRejected
)

type UploadTargetKind int

const (
	UploadTargetNewSubmission UploadTargetKind = iota + 1
	UploadTargetNewGroup
	UploadTargetExistingGroup
)

type RemovalReason int16

const (
	RemovalReasonOwnerDeleted RemovalReason = iota + 1
	RemovalReasonAdministratorQuarantined
	RemovalReasonAdministratorDeleted
	RemovalReasonAccountPurged
)

type Quota int

const (
	QuotaSignedURLs Quota = iota + 1
	QuotaSubmissionImages
	QuotaRollingDayBytes
	QuotaStoredBytes
	QuotaObjectBytes
)

type QuotaError struct {
	Quota    Quota
	Limit    uint64
	Observed uint64
	RetryAt  time.Time
}

func (e *QuotaError) Error() string { return "upload quota exhausted" }

type UploadFailure int

const (
	UploadFailureReservationMissing UploadFailure = iota + 1
	UploadFailureReservationExpired
	UploadFailureBindingMismatch
	UploadFailureStagingObjectMissing
	UploadFailureStagingObjectChanged
	UploadFailureSizeMismatch
	UploadFailureChecksumMismatch
	UploadFailureInvalidContentType
	UploadFailureInvalidPNGSignature
	UploadFailureUnsafeDimensions
	UploadFailureAlreadyFinalized
	UploadFailureInvalidState
)

type UploadError struct{ Failure UploadFailure }

func (e *UploadError) Error() string { return "upload cannot be finalized" }

var (
	ErrOperationLeaseLost             = errors.New("upload operation lease lost")
	ErrUploadRemovalPendingCompletion = errors.New("upload removal is pending completion")
	ErrObjectNotFound                 = errors.New("upload object not found")
	ErrObjectPrecondition             = errors.New("upload object precondition failed")
)

type UploadTarget struct {
	Kind          UploadTargetKind
	SubmissionID  string
	UploadGroupID string
}

type CreateUpload struct {
	OwnerUserID string
	Target      UploadTarget
	SizeBytes   uint64
	SHA256      [32]byte
	Now         time.Time
}

type SignedPUT struct {
	URL                  string
	ContentType          string
	ChecksumSHA256Base64 string
}

type UploadReservation struct {
	UploadID           string
	OwnerUserID        string
	SubmissionID       string
	UploadGroupID      string
	ReservationID      string
	PublicID           string
	StagingID          string
	StagingGeneration  uint64
	SizeBytes          uint64
	SHA256             [32]byte
	CreatedAt          time.Time
	SignedURLExpiresAt time.Time
	StagingExpiresAt   time.Time
	SignedPUT          SignedPUT
}

func (r UploadReservation) StagingKey() string {
	return "staging/" + r.StagingID + "/" + formatGeneration(r.StagingGeneration) + ".png"
}

func (r UploadReservation) PublicKey() string { return r.PublicID + ".png" }

type UploadBinding struct {
	UploadID          string
	SubmissionID      string
	UploadGroupID     string
	ReservationID     string
	StagingGeneration uint64
	SizeBytes         uint64
	SHA256            [32]byte
	ObservedETag      string
}

type Upload struct {
	UploadReservation
	State           UploadState
	StagingETag     string
	PublicETag      string
	ReplacementETag string
	Width           uint32
	Height          uint32
	FinalizedAt     *time.Time
	RemovedAt       *time.Time
	RemovalReason   RemovalReason
	OperationToken  string
	RemovalAudit    *AdministratorUploadAudit
}

type UploadCursor struct {
	CreatedAt time.Time
	UploadID  string
}

type UploadList struct {
	Uploads []Upload
	Next    *UploadCursor
}

type AdminUploadFilters struct {
	OwnerUserID   string
	SubmissionID  string
	UploadGroupID string
	States        []UploadState
}

type UploadUsage struct {
	SignedURLsRollingHour uint64
	UploadBytesRollingDay uint64
	StoredBytes           uint64
	FinalizedImages       uint64
	SubmissionImages      map[string]uint64
}

type AdministratorUploadAudit struct {
	ActorUserID string
	Rationale   string
	Event       AuditEvent
}

type StagingSweepResult struct {
	Claimed int
	Deleted int
}

type UploadObject struct {
	ETag        string
	SizeBytes   uint64
	ContentType string
	Checksum    []byte
	Header      []byte
}

type UploadSigner interface {
	PresignPUT(context.Context, UploadReservation) (SignedPUT, error)
}

type UploadObjectStore interface {
	InspectStaging(context.Context, UploadReservation) (UploadObject, error)
	Promote(context.Context, Upload, string) (string, error)
	DeleteStaging(context.Context, UploadReservation) error
	ReplacePublic(context.Context, Upload, []byte) (string, error)
}

type UploadStorage interface {
	UploadSigner
	UploadObjectStore
}

type UploadCache interface {
	PurgeAndRevalidate(context.Context, string, []byte) error
}

type UploadRepository interface {
	CreateUpload(context.Context, CreateUpload, func(context.Context, UploadReservation) (SignedPUT, error)) (UploadReservation, error)
	GetUploadForFinalize(context.Context, string, UploadBinding, time.Time) (Upload, error)
	ClaimUploadPromotion(context.Context, string, UploadBinding, UploadObject, uint32, uint32, string, time.Time) (Upload, error)
	CompleteUploadPromotion(context.Context, string, string, string, time.Time) (Upload, error)
	ReleaseUploadPromotion(context.Context, string, string) error
	RejectUpload(context.Context, string, UploadBinding, UploadFailure, time.Time) error
	ListUploads(context.Context, string, []UploadState, string, *UploadCursor, uint32) (UploadList, error)
	ListUploadsForAdministrator(context.Context, AdminUploadFilters, *UploadCursor, uint32) (UploadList, error)
	ClaimUploadRemoval(context.Context, string, string, RemovalReason, UploadState, *AdministratorUploadAudit, string, time.Time) (Upload, error)
	RecordUploadReplacement(context.Context, string, string, string) (Upload, error)
	CompleteUploadRemoval(context.Context, string, string, time.Time) (Upload, error)
	ReleaseUploadRemoval(context.Context, string, string) error
	ClaimExpiredUploads(context.Context, time.Time, int) ([]Upload, error)
	CompleteExpiredUpload(context.Context, string, time.Time) error
	ListAccountUploadsForPurge(context.Context, string, int) ([]Upload, error)
	RemoveAccountUploadMetadata(context.Context, string) error
	GetUploadUsage(context.Context, string, time.Time) (UploadUsage, error)
}

type UploadStagingSweeper interface {
	SweepExpiredUploads(context.Context, time.Time, int) (StagingSweepResult, error)
}

// UploadAdministration is deliberately an internal hook. AdminService wiring
// is owned by the administrator milestone and must not expose object bytes.
type UploadAdministration interface {
	ListUploads(context.Context, string, AdminUploadFilters, string, uint32) (UploadList, string, error)
	GetUsage(context.Context, string) (UploadUsage, error)
	RemoveUpload(context.Context, string, string, RemovalReason, UploadState, string, AuditEvent) (Upload, error)
}

func formatGeneration(value uint64) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	position := len(buffer)
	for value > 0 {
		position--
		buffer[position] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[position:])
}
