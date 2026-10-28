// SPDX-License-Identifier: Apache-2.0
// Package artifacts manages temporary generated media. Authorization and durable
// lifecycle transitions belong to the injected repository, not an in-process cache.
package artifacts

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
)

const (
	MaxImageBytes         = 50 << 20
	MaxResponseBytes      = 100 << 20
	MaxVideoBytes         = 200 << 20
	MaxVideoResponseBytes = 600 << 20
	MaxFiles              = 200
	Lifetime              = 24 * time.Hour
	MaxSignedLifetime     = 5 * time.Minute
	UploadTimeout         = 5 * time.Minute
)

var (
	ErrNotFound = errors.New("artifact unavailable")
	ErrLimit    = errors.New("artifact size or count limit exceeded")
	ErrInvalid  = errors.New("invalid artifact")
)

type Owner struct {
	OrganizationID, UserID string
	Generation             int64
}
type Artifact struct {
	ID, ObjectKey                        string
	Owner                                Owner
	ContentType                          string
	Size                                 int64
	CreatedAt, ExpiresAt, UploadDeadline time.Time
}
type Image struct {
	Data        []byte
	ContentType string
}
type Deletion struct {
	ID, ObjectKey string
	NotBefore     time.Time
}

// Repository operations must be durable and atomic. All owner operations check
// live membership AND its generation. IDs/keys are never reused. Reserve stores
// inaccessible pending rows and deletion intents before any object upload.
// Publish rechecks the fence, deadline and pending state, activates the whole
// batch, cancels its upload intents, and evicts oldest CreatedAt/ID rows above
// MaxFiles per owner. Eviction, expiry and owner deletion atomically hide rows
// and enqueue object deletion. Outbox rows must survive owner FK deletion.
// Pending upload deletions cannot be acknowledged before UploadDeadline.
// Get denies pending, tombstoned and expired rows even before physical cleanup.
// CompleteDelete acknowledges only the exact immutable deletion ID/key. Failed
// or ambiguous operations retain outbox work; no transition revives a tombstone.
type Repository interface {
	Reserve(context.Context, Owner, []Artifact, time.Time) error
	Publish(context.Context, Owner, []string, time.Time) error
	Get(context.Context, Owner, string, time.Time) (Artifact, error)
	Tombstone(context.Context, Owner, []string, time.Time) error
	Expire(context.Context, time.Time) error
	PendingDeletes(context.Context, time.Time, int) ([]Deletion, error)
	CompleteDelete(context.Context, Deletion, time.Time) error
}

// Put must honor its context deadline and must not schedule asynchronous retries
// after returning. Delete is idempotent. PresignGet must round lifetime down.
type ObjectStore interface {
	Put(context.Context, string, string, []byte) error
	Delete(context.Context, string) error
	PresignGet(context.Context, string, time.Duration) (string, error)
}
type Service struct {
	repo    Repository
	objects ObjectStore
	now     func() time.Time
}

func New(repo Repository, objects ObjectStore) *Service {
	return &Service{repo: repo, objects: objects, now: time.Now}
}
func validOwner(o Owner) bool { return o.OrganizationID != "" && o.UserID != "" && o.Generation > 0 }
func (s *Service) StoreResponse(ctx context.Context, owner Owner, images []Image) ([]Artifact, error) {
	return s.storeResponse(ctx, owner, images, false)
}

// Video is a decoded MP4. Callers must also enforce the per-turn cumulative
// video budget when a hosted turn publishes more than one response batch.
// The alias keeps existing hosted media byte collectors compatible. The method,
// not the container type, selects the MIME and byte-limit policy.
type Video = Image

func (s *Service) StoreVideoResponse(ctx context.Context, owner Owner, videos []Video) ([]Artifact, error) {
	return s.storeResponse(ctx, owner, videos, true)
}
func (s *Service) storeResponse(ctx context.Context, owner Owner, images []Image, video bool) ([]Artifact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fileLimit, batchLimit := MaxImageBytes, MaxResponseBytes
	if video {
		fileLimit, batchLimit = MaxVideoBytes, MaxVideoResponseBytes
	}
	if !validOwner(owner) || len(images) == 0 {
		return nil, ErrInvalid
	}
	if len(images) > MaxFiles {
		return nil, ErrLimit
	}
	var total int64
	for _, im := range images {
		if len(im.Data) == 0 {
			return nil, ErrInvalid
		}
		if len(im.Data) > fileLimit {
			return nil, ErrLimit
		}
		if video {
			if im.ContentType != "video/mp4" {
				return nil, ErrInvalid
			}
		} else {
			switch im.ContentType {
			case "image/png", "image/jpeg", "image/webp", "image/gif", "image/avif":
			default:
				return nil, ErrInvalid
			}
		}
		total += int64(len(im.Data))
		if total > int64(batchLimit) {
			return nil, ErrLimit
		}
	}
	now := s.now()
	rows := make([]Artifact, len(images))
	ids := make([]string, len(images))
	for i, im := range images {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		ids[i] = id.String()
		rows[i] = Artifact{ID: ids[i], ObjectKey: "artifacts/" + ids[i], Owner: owner, ContentType: im.ContentType, Size: int64(len(im.Data)), CreatedAt: now, ExpiresAt: now.Add(Lifetime), UploadDeadline: now.Add(UploadTimeout)}
	}
	if err := s.repo.Reserve(ctx, owner, rows, now); err != nil {
		return nil, err
	}
	uploadCtx, cancel := context.WithDeadline(ctx, now.Add(UploadTimeout))
	defer cancel()
	for i, row := range rows {
		if err := uploadCtx.Err(); err != nil {
			return nil, s.abort(ctx, owner, ids, err)
		}
		if err := s.objects.Put(uploadCtx, row.ObjectKey, row.ContentType, images[i].Data); err != nil {
			return nil, s.abort(ctx, owner, ids, err)
		}
	}
	if err := uploadCtx.Err(); err != nil {
		return nil, s.abort(ctx, owner, ids, err)
	}
	if err := s.repo.Publish(uploadCtx, owner, ids, s.now()); err != nil {
		return nil, s.abort(ctx, owner, ids, err)
	}
	return rows, nil
}
func (s *Service) abort(ctx context.Context, owner Owner, ids []string, cause error) error {
	// Cleanup must survive a disconnected caller. Reserve already persisted retry
	// intent, so a database outage here cannot strand an untracked object.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return errors.Join(cause, s.repo.Tombstone(cleanup, owner, ids, s.now()))
}
func (s *Service) SignedURL(ctx context.Context, owner Owner, id string) (string, error) {
	if !validOwner(owner) {
		return "", ErrNotFound
	}
	row, err := s.repo.Get(ctx, owner, id, s.now())
	if err != nil {
		return "", err
	}
	if row.Owner != owner || row.ID != id {
		return "", ErrNotFound
	}
	issuedAt := s.now()
	ttl := row.ExpiresAt.Sub(issuedAt).Truncate(time.Second)
	if ttl > MaxSignedLifetime {
		ttl = MaxSignedLifetime
	}
	if ttl < time.Second {
		return "", ErrNotFound
	}
	signed, err := s.objects.PresignGet(ctx, row.ObjectKey, ttl)
	if err != nil {
		return "", err
	}
	// Recheck after signing to catch removal while the signer runs.
	current, err := s.repo.Get(ctx, owner, id, s.now())
	if err != nil {
		return "", err
	}
	if current != row || !s.now().Before(row.ExpiresAt) {
		return "", ErrNotFound
	}
	u, err := url.Parse(signed)
	if err != nil {
		return "", ErrInvalid
	}
	signedAt, err := time.Parse("20060102T150405Z", u.Query().Get("X-Amz-Date"))
	if err != nil {
		return "", ErrInvalid
	}
	seconds, err := strconv.ParseInt(u.Query().Get("X-Amz-Expires"), 10, 64)
	if err != nil || seconds < 1 || seconds > 300 {
		return "", ErrInvalid
	}
	expires := signedAt.Add(time.Duration(seconds) * time.Second)
	if expires.After(row.ExpiresAt) || expires.After(issuedAt.Add(MaxSignedLifetime)) || !s.now().Before(expires) {
		return "", ErrNotFound
	}
	return signed, nil
}
func (s *Service) Delete(ctx context.Context, owner Owner, id string) error {
	if !validOwner(owner) {
		return ErrNotFound
	}
	return s.repo.Tombstone(ctx, owner, []string{id}, s.now())
}

// Cleanup processes a bounded batch. The scheduler calls it every minute and
// records failures; unsuccessful deletions remain due for the next invocation.
func (s *Service) Cleanup(ctx context.Context) error {
	now := s.now()
	if err := s.repo.Expire(ctx, now); err != nil {
		return err
	}
	jobs, err := s.repo.PendingDeletes(ctx, now, 200)
	if err != nil {
		return err
	}
	var failures []error
	for _, job := range jobs {
		if now.Before(job.NotBefore) {
			continue
		}
		if err := s.objects.Delete(ctx, job.ObjectKey); err != nil {
			failures = append(failures, fmt.Errorf("artifact object deletion: %w", err))
			continue
		}
		if err := s.repo.CompleteDelete(ctx, job, s.now()); err != nil {
			failures = append(failures, fmt.Errorf("artifact deletion acknowledgement: %w", err))
		}
	}
	return errors.Join(failures...)
}
