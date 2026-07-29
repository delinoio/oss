package imageassets

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryObjects struct {
	mu      sync.Mutex
	objects map[string]memoryObject
}

type memoryObject struct {
	contentType string
	body        []byte
}

func (store *memoryObjects) Put(
	_ context.Context,
	key string,
	contentType string,
	body []byte,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.objects == nil {
		store.objects = make(map[string]memoryObject)
	}
	store.objects[key] = memoryObject{
		contentType: contentType, body: append([]byte(nil), body...),
	}
	return nil
}

func (store *memoryObjects) Get(
	_ context.Context,
	key string,
) (Object, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.objects[key]
	if !ok {
		return Object{}, ErrObjectNotFound
	}
	return Object{
		Body:        io.NopCloser(bytes.NewReader(value.body)),
		ContentType: value.contentType, Size: int64(len(value.body)),
	}, nil
}

func (store *memoryObjects) Delete(_ context.Context, key string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.objects, key)
	return nil
}

func TestSignedPUTIsScopedShortLivedAndStoresPrivately(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	signer, err := NewSigner(
		"https://assets.realqa.deli.dev", bytes.Repeat([]byte("s"), 32))
	if err != nil {
		t.Fatal(err)
	}
	signer.random = func(value []byte) (int, error) {
		for index := range value {
			value[index] = byte(index + 1)
		}
		return len(value), nil
	}
	body := pngFixture(t)
	declaration := declarationFor(body, MediaTypePNG, 4, 3)
	signed, err := signer.Sign(
		now, now.Add(2*time.Minute), "submission", "asset", declaration)
	if err != nil {
		t.Fatal(err)
	}
	if signed.ExpiresAt != now.Add(2*time.Minute) ||
		!strings.HasPrefix(signed.URL,
			"https://assets.realqa.deli.dev/uploads/") ||
		strings.Contains(signed.URL, "r2.cloudflarestorage.com") {
		t.Fatalf("signed PUT = %#v", signed)
	}
	grant := Grant{
		TokenDigest: signed.TokenDigest, SubmissionID: "submission",
		AssetID: "asset", Declaration: declaration,
		ExpiresAt: signed.ExpiresAt, Deadline: now.Add(2 * time.Minute),
	}
	objects := &memoryObjects{}
	uploaded := false
	handler := UploadHandler(
		signer, objects,
		func(_ context.Context, digest [32]byte) (Grant, error) {
			if digest != signed.TokenDigest {
				return Grant{}, errors.New("not found")
			}
			return grant, nil
		},
		func(_ context.Context, value Grant) error {
			uploaded = value.AssetID == "asset"
			return nil
		},
		func() time.Time { return now.Add(time.Minute) },
	)
	request := httptest.NewRequest(http.MethodPut, signed.URL, bytes.NewReader(body))
	request.Header.Set("Content-Type", string(MediaTypePNG))
	request.Header.Set(ContentSHA256Header, declaration.SHA256)
	request.ContentLength = int64(len(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || !uploaded {
		t.Fatalf("upload response = %d %q", response.Code, response.Body)
	}
	if _, err = objects.Get(
		context.Background(), StagingObjectKey("asset")); err != nil {
		t.Fatal(err)
	}

	tampered := httptest.NewRequest(
		http.MethodPut, signed.URL, bytes.NewReader(body))
	tampered.Header.Set("Content-Type", string(MediaTypeWebP))
	tampered.Header.Set(ContentSHA256Header, declaration.SHA256)
	tampered.ContentLength = int64(len(body))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, tampered)
	if response.Code != http.StatusForbidden {
		t.Fatalf("tampered scope response = %d", response.Code)
	}
}

func TestSignedPUTRejectsExpiryAndExtraScope(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	signer, err := NewSigner(
		"https://assets.realqa.deli.dev", bytes.Repeat([]byte("s"), 32))
	if err != nil {
		t.Fatal(err)
	}
	body := pngFixture(t)
	declaration := declarationFor(body, MediaTypePNG, 4, 3)
	signed, err := signer.Sign(
		now, now.Add(time.Hour), "submission", "asset", declaration)
	if err != nil {
		t.Fatal(err)
	}
	grant := Grant{
		TokenDigest: signed.TokenDigest, SubmissionID: "submission",
		AssetID: "asset", Declaration: declaration,
		ExpiresAt: signed.ExpiresAt, Deadline: now.Add(time.Hour),
	}
	request := httptest.NewRequest(http.MethodPut, signed.URL, bytes.NewReader(body))
	request.Header.Set("Content-Type", string(MediaTypePNG))
	request.Header.Set(ContentSHA256Header, declaration.SHA256)
	request.ContentLength = int64(len(body))
	if err = signer.VerifyRequest(
		request, grant, signed.ExpiresAt); !errors.Is(err, ErrExpired) {
		t.Fatalf("expiry error = %v", err)
	}
	request.URL.RawQuery += "&unexpected=1"
	if err = signer.VerifyRequest(
		request, grant, now); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("extra query error = %v", err)
	}
	request.URL.RawQuery = strings.TrimSuffix(
		request.URL.RawQuery, "&unexpected=1")
	request.Host = "attacker.example"
	if err = signer.VerifyRequest(
		request, grant, now); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("host substitution error = %v", err)
	}
}

func TestIdempotentSignedPUTReplaysWithoutPersistingBearer(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(time.Hour)
	signer, err := NewSigner(
		"https://assets.realqa.deli.dev", bytes.Repeat([]byte("s"), 32))
	if err != nil {
		t.Fatal(err)
	}
	body := pngFixture(t)
	declaration := declarationFor(body, MediaTypePNG, 4, 3)
	signed, err := signer.SignIdempotent(
		now, deadline, "submission", "asset", declaration, "idempotency")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := signer.ReplayIdempotent(
		signed.ExpiresAt, deadline, "submission", "asset",
		declaration, "idempotency")
	if err != nil {
		t.Fatal(err)
	}
	if replayed.URL != signed.URL ||
		replayed.TokenDigest != signed.TokenDigest ||
		replayed.ExpiresAt != signed.ExpiresAt {
		t.Fatalf("replayed signed PUT = %#v, want %#v", replayed, signed)
	}
	other, err := signer.SignIdempotent(
		now, deadline, "submission", "asset", declaration, "other")
	if err != nil {
		t.Fatal(err)
	}
	if other.URL == signed.URL || other.TokenDigest == signed.TokenDigest {
		t.Fatal("distinct idempotency keys produced the same signed PUT")
	}
}

func TestUploadStateFailurePreservesSharedStagingObject(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	signer, err := NewSigner(
		"https://assets.realqa.deli.dev", bytes.Repeat([]byte("s"), 32))
	if err != nil {
		t.Fatal(err)
	}
	body := pngFixture(t)
	declaration := declarationFor(body, MediaTypePNG, 4, 3)
	signed, err := signer.Sign(
		now, now.Add(time.Hour), "submission", "asset", declaration)
	if err != nil {
		t.Fatal(err)
	}
	grant := Grant{
		TokenDigest: signed.TokenDigest, SubmissionID: "submission",
		AssetID: "asset", Declaration: declaration,
		ExpiresAt: signed.ExpiresAt, Deadline: now.Add(time.Hour),
	}
	objects := &memoryObjects{}
	handler := UploadHandler(
		signer, objects,
		func(_ context.Context, _ [32]byte) (Grant, error) {
			return grant, nil
		},
		func(_ context.Context, _ Grant) error {
			return errors.New("concurrent completion")
		},
		func() time.Time { return now.Add(time.Minute) },
	)
	request := httptest.NewRequest(http.MethodPut, signed.URL, bytes.NewReader(body))
	request.Header.Set("Content-Type", string(MediaTypePNG))
	request.Header.Set(ContentSHA256Header, declaration.SHA256)
	request.ContentLength = int64(len(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("upload response = %d", response.Code)
	}
	if _, err = objects.Get(
		context.Background(), StagingObjectKey("asset")); err != nil {
		t.Fatalf("shared staging object was removed: %v", err)
	}
}

func TestPublicGETBecomesPlaceholderAtSameURL(t *testing.T) {
	t.Parallel()
	objects := &memoryObjects{}
	signer, err := NewSigner(
		"https://assets.realqa.deli.dev", bytes.Repeat([]byte("s"), 32))
	if err != nil {
		t.Fatal(err)
	}
	publicID := "abcdefghijklmnopqrstuv"
	body := pngFixture(t)
	if err = objects.Put(
		context.Background(), PublicObjectKey(publicID),
		string(MediaTypePNG), body); err != nil {
		t.Fatal(err)
	}
	state := PublicStateRetained
	handler := PublicHandler(signer, objects, func(
		_ context.Context,
		value string,
	) (PublicRecord, error) {
		if value != publicID {
			return PublicRecord{}, ErrObjectNotFound
		}
		return PublicRecord{
			State: state, ObjectKey: PublicObjectKey(publicID),
			ContentType: string(MediaTypePNG),
		}, nil
	})
	path := "/i/" + publicID
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Host = "assets.realqa.deli.dev"
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		response.Header().Get("Content-Type") != string(MediaTypePNG) ||
		!bytes.Equal(response.Body.Bytes(), body) {
		t.Fatalf("public image response = %d %q", response.Code, response.Body)
	}
	state = PublicStateRemoved
	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, path, nil)
	request.Host = "assets.realqa.deli.dev"
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		response.Header().Get("Content-Type") != "image/svg+xml" ||
		!strings.Contains(response.Body.String(), "Image removed") ||
		strings.Contains(response.Body.String(), publicID) {
		t.Fatalf("placeholder response = %d %q", response.Code, response.Body)
	}
	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, path, nil)
	request.Host = "realqa.deli.dev"
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("API-origin public image response = %d", response.Code)
	}
}

func TestPublicIDsHave128BitsAndR2HasNoListingSurface(t *testing.T) {
	t.Parallel()
	seen := make(map[string]struct{}, 256)
	for range 256 {
		value, err := NewPublicID()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil || len(raw) != 16 || len(value) != 22 {
			t.Fatalf("public ID %q has %d decoded bytes", value, len(raw))
		}
		if _, duplicate := seen[value]; duplicate {
			t.Fatalf("duplicate public ID %q", value)
		}
		seen[value] = struct{}{}
	}
	storeType := reflect.TypeOf((*R2Store)(nil))
	methods := make([]string, 0, storeType.NumMethod())
	for index := range storeType.NumMethod() {
		methods = append(methods, storeType.Method(index).Name)
	}
	if !slices.Equal(methods, []string{"Delete", "Get", "Put"}) {
		t.Fatalf("R2 exported methods = %#v", methods)
	}
}
