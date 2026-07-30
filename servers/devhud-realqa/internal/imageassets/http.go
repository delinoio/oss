package imageassets

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrObjectNotFound      = errors.New("realqa images: object not found")
	ErrUploadGrantNotFound = errors.New("realqa images: upload grant not found")
)

type Object struct {
	Body        io.ReadCloser
	ContentType string
	Size        int64
}

type ObjectStore interface {
	Put(context.Context, string, string, []byte) error
	Get(context.Context, string) (Object, error)
	Delete(context.Context, string) error
}

type UploadLookup func(context.Context, [32]byte) (Grant, error)
type UploadStore func(context.Context, Grant, string, []byte) error

func UploadHandler(
	signer *Signer,
	lookup UploadLookup,
	store UploadStore,
	now func() time.Time,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if signer == nil || lookup == nil || store == nil {
			http.Error(writer, "upload unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := signer.VerifyRequestShape(request); err != nil {
			http.NotFound(writer, request)
			return
		}
		digest, err := TokenDigest(request.URL.Path)
		if err != nil {
			http.NotFound(writer, request)
			return
		}
		grant, err := lookup(request.Context(), digest)
		if err != nil {
			if errors.Is(err, ErrUploadGrantNotFound) {
				http.NotFound(writer, request)
			} else {
				http.Error(writer, "upload unavailable", http.StatusServiceUnavailable)
			}
			return
		}
		current := time.Now()
		if now != nil {
			current = now()
		}
		if err = signer.VerifyRequest(request, grant, current); err != nil {
			http.Error(writer, "upload authorization rejected", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(io.LimitReader(
			request.Body, grant.Declaration.EncodedBytes+1))
		if err != nil || int64(len(body)) != grant.Declaration.EncodedBytes {
			http.Error(writer, "upload rejected", http.StatusBadRequest)
			return
		}
		// Verify the received bytes before any write. Finalize performs the
		// independent decode/re-encode verification.
		if _, err = VerifySource(grant.Declaration, body); err != nil {
			http.Error(writer, "upload rejected", http.StatusBadRequest)
			return
		}
		if err = store(request.Context(), grant,
			string(grant.Declaration.MediaType), body); err != nil {
			http.Error(writer, "upload unavailable", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})
}

func VerifySource(declaration Declaration, body []byte) ([32]byte, error) {
	var zero [32]byte
	if int64(len(body)) != declaration.EncodedBytes ||
		int64(len(body)) > MaxImageEncodedBytes {
		return zero, ErrEncodedTooLarge
	}
	expected, err := decodeChecksum(declaration.SHA256)
	if err != nil {
		return zero, err
	}
	actual := sha256.Sum256(body)
	if !hmac.Equal(actual[:], expected) {
		return zero, ErrChecksum
	}
	if sniff(body) != declaration.MediaType {
		return zero, ErrMediaType
	}
	return actual, nil
}

func StagingObjectKey(assetID string) string {
	return "private/staging/" + assetID
}

func VerifiedObjectKey(assetID string) string {
	return "private/verified/" + assetID
}

func PublicObjectKey(publicID string) string {
	return "public/" + publicID
}

type PublicState uint8

const (
	PublicStateRetained PublicState = iota + 1
	PublicStateRemoved
)

type PublicRecord struct {
	State       PublicState
	ObjectKey   string
	ContentType string
}

type PublicLookup func(context.Context, string) (PublicRecord, error)

var publicIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22,128}$`)

var removedPlaceholder = []byte(
	`<svg xmlns="http://www.w3.org/2000/svg" width="640" height="360" role="img" aria-label="Image removed">` +
		`<rect width="100%" height="100%" fill="#f3f4f6"/>` +
		`<text x="50%" y="50%" text-anchor="middle" dominant-baseline="middle" ` +
		`font-family="system-ui,sans-serif" font-size="28" fill="#4b5563">Image removed</text></svg>`)

func PublicHandler(
	signer *Signer,
	objects ObjectStore,
	lookup PublicLookup,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.RawQuery != "" ||
			!signer.MatchesOriginHost(request) {
			http.NotFound(writer, request)
			return
		}
		publicID := strings.TrimPrefix(request.URL.Path, "/i/")
		if !strings.HasPrefix(request.URL.Path, "/i/") ||
			!publicIDPattern.MatchString(publicID) {
			http.NotFound(writer, request)
			return
		}
		record, err := lookup(request.Context(), publicID)
		if err != nil {
			if errors.Is(err, ErrObjectNotFound) {
				http.NotFound(writer, request)
			} else {
				writer.Header().Set("Cache-Control", "no-store")
				http.Error(writer, "image unavailable", http.StatusServiceUnavailable)
			}
			return
		}
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		// A short revalidation window bounds stale readability after a durable
		// tombstone without requiring a deployment-specific cache purge API.
		writer.Header().Set("Cache-Control", "public, max-age=60, must-revalidate")
		if record.State == PublicStateRemoved {
			writer.Header().Set("Content-Type", "image/svg+xml")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write(removedPlaceholder)
			return
		}
		if record.State != PublicStateRetained || objects == nil {
			http.NotFound(writer, request)
			return
		}
		object, err := objects.Get(request.Context(), record.ObjectKey)
		if err != nil {
			if errors.Is(err, ErrObjectNotFound) {
				http.NotFound(writer, request)
			} else {
				writer.Header().Set("Cache-Control", "no-store")
				http.Error(writer, "image unavailable", http.StatusServiceUnavailable)
			}
			return
		}
		defer object.Body.Close()
		if object.ContentType != record.ContentType ||
			(record.ContentType != string(MediaTypePNG) &&
				record.ContentType != string(MediaTypeWebP)) {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", record.ContentType)
		if object.Size >= 0 {
			writer.Header().Set("Content-Length", strconv.FormatInt(object.Size, 10))
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = io.Copy(writer, io.LimitReader(object.Body, MaxImageEncodedBytes+1))
	})
}
