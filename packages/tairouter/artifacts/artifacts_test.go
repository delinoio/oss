// SPDX-License-Identifier: Apache-2.0
package artifacts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// This repository models the documented transaction boundary, not PostgreSQL.
// Store integration tests must separately verify the real adapter's transactions.
type testRepo struct {
	mu         sync.Mutex
	rows       map[string]Artifact
	active     map[string]bool
	jobs       map[string]Deletion
	generation map[string]int64
	failAck    bool
}

func newRepo() *testRepo {
	return &testRepo{rows: map[string]Artifact{}, active: map[string]bool{}, jobs: map[string]Deletion{}, generation: map[string]int64{"org/user": 1, "other/user": 1, "org/other": 1}}
}
func (r *testRepo) check(o Owner) error {
	if r.generation[o.OrganizationID+"/"+o.UserID] != o.Generation {
		return ErrNotFound
	}
	return nil
}
func (r *testRepo) Reserve(_ context.Context, o Owner, rows []Artifact, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.check(o); err != nil {
		return err
	}
	for _, row := range rows {
		if _, ok := r.rows[row.ID]; ok {
			return ErrInvalid
		}
	}
	for _, row := range rows {
		r.rows[row.ID] = row
		r.jobs[row.ID] = Deletion{row.ID, row.ObjectKey, row.UploadDeadline}
	}
	return nil
}
func (r *testRepo) Publish(_ context.Context, o Owner, ids []string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.check(o); err != nil {
		return err
	}
	for _, id := range ids {
		row, ok := r.rows[id]
		if !ok || row.Owner != o || !now.Before(row.UploadDeadline) || !now.Before(row.ExpiresAt) {
			return ErrNotFound
		}
		if _, ok := r.jobs[id]; !ok {
			return ErrNotFound
		}
	}
	for _, id := range ids {
		r.active[id] = true
		delete(r.jobs, id)
	}
	var rows []Artifact
	for id, row := range r.rows {
		if r.active[id] && row.Owner == o {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].CreatedAt.Before(rows[j].CreatedAt)
	})
	for len(rows) > MaxFiles {
		row := rows[0]
		r.hide(row, now)
		rows = rows[1:]
	}
	return nil
}
func (r *testRepo) Get(_ context.Context, o Owner, id string, now time.Time) (Artifact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.check(o); err != nil {
		return Artifact{}, err
	}
	a, ok := r.rows[id]
	if !ok || a.Owner != o || !r.active[id] || !now.Before(a.ExpiresAt) {
		return Artifact{}, ErrNotFound
	}
	return a, nil
}
func (r *testRepo) hide(a Artifact, now time.Time) {
	if !r.active[a.ID] && now.Before(a.UploadDeadline) {
		now = a.UploadDeadline
	}
	delete(r.active, a.ID)
	r.jobs[a.ID] = Deletion{a.ID, a.ObjectKey, now}
}
func (r *testRepo) Tombstone(_ context.Context, o Owner, ids []string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.check(o); err != nil {
		return err
	}
	for _, id := range ids {
		if row, ok := r.rows[id]; ok && row.Owner == o {
			r.hide(row, now)
			delete(r.rows, id)
		}
	}
	return nil
}
func (r *testRepo) Expire(_ context.Context, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, a := range r.rows {
		if !now.Before(a.ExpiresAt) || (!r.active[id] && !now.Before(a.UploadDeadline)) {
			r.hide(a, now)
		}
	}
	return nil
}
func (r *testRepo) PendingDeletes(_ context.Context, now time.Time, limit int) ([]Deletion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var jobs []Deletion
	for _, j := range r.jobs {
		if !now.Before(j.NotBefore) {
			jobs = append(jobs, j)
		}
		if len(jobs) == limit {
			break
		}
	}
	return jobs, nil
}
func (r *testRepo) CompleteDelete(_ context.Context, j Deletion, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failAck {
		return errors.New("DB unavailable")
	}
	if current, ok := r.jobs[j.ID]; ok && current == j && !now.Before(current.NotBefore) {
		delete(r.jobs, j.ID)
		delete(r.rows, j.ID)
	}
	return nil
}

type testObjects struct {
	now                 *time.Time
	data                map[string][]byte
	putHook             func()
	failPut, failDelete bool
	deletes             int
	ttl                 time.Duration
	signHook            func()
}

func (o *testObjects) Put(ctx context.Context, k, ct string, b []byte) error {
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("missing upload deadline")
	}
	o.data[k] = append([]byte(nil), b...)
	if o.putHook != nil {
		o.putHook()
	}
	if o.failPut {
		return errors.New("ambiguous upload")
	}
	return nil
}
func (o *testObjects) Delete(_ context.Context, k string) error {
	o.deletes++
	if o.failDelete {
		return errors.New("R2 unavailable")
	}
	delete(o.data, k)
	return nil
}
func (o *testObjects) PresignGet(_ context.Context, k string, ttl time.Duration) (string, error) {
	o.ttl = ttl
	if o.signHook != nil {
		o.signHook()
	}
	return "https://bucket.example/" + k + "?X-Amz-Date=" + o.now.UTC().Format("20060102T150405Z") + "&X-Amz-Expires=" + strconv.Itoa(int(ttl/time.Second)), nil
}
func setup() (*Service, *testRepo, *testObjects, *time.Time, Owner) {
	now := time.Now().UTC().Truncate(time.Second)
	repo := newRepo()
	objects := &testObjects{now: &now, data: map[string][]byte{}}
	s := New(repo, objects)
	s.now = func() time.Time { return now }
	return s, repo, objects, &now, Owner{"org", "user", 1}
}
func TestLimitsAndOwnership(t *testing.T) {
	s, r, o, _, owner := setup()
	ctx := context.Background()
	image := Image{Data: []byte("image"), ContentType: "image/png"}
	for _, imgs := range [][]Image{{{Data: make([]byte, MaxImageBytes+1), ContentType: "image/png"}}, {{Data: make([]byte, MaxImageBytes), ContentType: "image/png"}, {Data: make([]byte, MaxImageBytes), ContentType: "image/png"}, image}, make([]Image, MaxFiles+1)} {
		if _, err := s.StoreResponse(ctx, owner, imgs); !errors.Is(err, ErrLimit) {
			t.Fatalf("expected limit, got %v", err)
		}
	}
	if len(r.rows) != 0 || len(o.data) != 0 {
		t.Fatal("invalid response caused side effects")
	}
	rows, err := s.StoreResponse(ctx, owner, []Image{image})
	if err != nil {
		t.Fatal(err)
	}
	a := rows[0]
	for _, wrong := range []Owner{{"other", "user", 1}, {"org", "other", 1}, {"org", "user", 2}} {
		if _, err = s.SignedURL(ctx, wrong, a.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("ownership bypass", wrong, err)
		}
	}
	if _, err = s.SignedURL(ctx, owner, a.ID); err != nil || o.ttl != MaxSignedLifetime {
		t.Fatal(err, o.ttl)
	}
	if a.ExpiresAt.Sub(a.CreatedAt) != Lifetime {
		t.Fatal(a)
	}
	// Exact inclusive boundaries are accepted.
	if _, err = s.StoreResponse(ctx, owner, []Image{{Data: make([]byte, MaxImageBytes), ContentType: "image/png"}, {Data: make([]byte, MaxImageBytes), ContentType: "image/png"}}); err != nil {
		t.Fatal(err)
	}
}
func TestExpiryEvictionAndDeleteRetry(t *testing.T) {
	s, r, o, now, owner := setup()
	ctx := context.Background()
	imgs := make([]Image, MaxFiles)
	for i := range imgs {
		imgs[i] = Image{[]byte("x"), "image/png"}
	}
	rows, err := s.StoreResponse(ctx, owner, imgs)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Second)
	if _, err = s.StoreResponse(ctx, owner, imgs[:1]); err != nil {
		t.Fatal(err)
	}
	if len(r.active) != 200 {
		t.Fatal(len(r.active))
	}
	if _, err = s.SignedURL(ctx, owner, rows[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("oldest not evicted", err)
	}
	o.failDelete = true
	if err = s.Cleanup(ctx); err == nil {
		t.Fatal("deletion failure hidden")
	}
	if len(r.jobs) != 1 {
		t.Fatal("outbox lost")
	}
	o.failDelete = false
	r.failAck = true
	if err = s.Cleanup(ctx); err == nil {
		t.Fatal("ack failure hidden")
	}
	r.failAck = false
	if err = s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.jobs) != 0 || o.deletes != 3 {
		t.Fatal("retry failed")
	}
	*now = rows[1].ExpiresAt.Add(-2500 * time.Millisecond)
	if _, err = s.SignedURL(ctx, owner, rows[1].ID); err != nil || o.ttl != 2*time.Second {
		t.Fatal(err, o.ttl)
	}
	*now = rows[1].ExpiresAt
	if _, err = s.SignedURL(ctx, owner, rows[1].ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired row visible", err)
	}
	if err = s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestUploadFailureAndRemovalFence(t *testing.T) {
	s, r, o, now, owner := setup()
	ctx := context.Background()
	o.failPut = true
	if _, err := s.StoreResponse(ctx, owner, []Image{{[]byte("x"), "image/png"}}); err == nil {
		t.Fatal("failure hidden")
	}
	if len(r.active) != 0 || len(r.jobs) != 1 {
		t.Fatal("missing durable cleanup")
	}
	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if o.deletes != 0 {
		t.Fatal("deleted before pending upload deadline")
	}
	*now = now.Add(UploadTimeout)
	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if len(o.data) != 0 || len(r.jobs) != 0 {
		t.Fatal("failed upload leaked")
	}
	o.failPut = false
	o.putHook = func() { r.generation["org/user"] = 2 }
	if _, err := s.StoreResponse(ctx, owner, []Image{{[]byte("late"), "image/png"}}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if len(r.active) != 0 {
		t.Fatal("removed member published")
	}
	*now = now.Add(UploadTimeout)
	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if len(o.data) != 0 {
		t.Fatal("late completion leaked")
	}
}
func TestSignerDelayAndReauthorization(t *testing.T) {
	s, r, o, now, owner := setup()
	ctx := context.Background()
	rows, err := s.StoreResponse(ctx, owner, []Image{{[]byte("x"), "image/png"}})
	if err != nil {
		t.Fatal(err)
	}
	o.signHook = func() { r.generation["org/user"] = 2 }
	if _, err = s.SignedURL(ctx, owner, rows[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("signer bypassed removal", err)
	}
	r.generation["org/user"] = 1
	*now = rows[0].ExpiresAt.Add(-2 * time.Second)
	o.signHook = func() { *now = now.Add(time.Second) }
	if _, err = s.SignedURL(ctx, owner, rows[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("signature exceeded artifact expiry", err)
	}
}
func TestR2SDKWireAndSigning(t *testing.T) {
	var puts, deletes int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("SDK signature missing")
		}
		if r.URL.Path != "/bucket/artifacts/test" {
			t.Error(r.URL.Path)
		}
		switch r.Method {
		case "PUT":
			puts++
			if r.Header.Get("Content-Type") != "image/png" {
				t.Error("content type missing")
			}
		case "DELETE":
			deletes++
		default:
			t.Error(r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := s3.New(s3.Options{Region: "auto", BaseEndpoint: aws.String(server.URL), UsePathStyle: true, HTTPClient: server.Client(), Credentials: credentials.NewStaticCredentialsProvider("test-access", "test-secret", ""), RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired})
	r := &R2{client: client, signer: s3.NewPresignClient(client), bucket: "bucket"}
	ctx := context.Background()
	if err := r.Put(ctx, "artifacts/test", "image/png", []byte("image")); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, "artifacts/test"); err != nil {
		t.Fatal(err)
	}
	signed, err := r.PresignGet(ctx, "artifacts/test", 2500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(signed)
	if u.Query().Get("X-Amz-Expires") != "2" || u.Query().Get("X-Amz-Signature") == "" {
		t.Fatal("invalid SDK presign")
	}
	if puts != 1 || deletes != 1 {
		t.Fatal(puts, deletes)
	}
	for _, ttl := range []time.Duration{0, time.Millisecond, MaxSignedLifetime + time.Second} {
		if _, err = r.PresignGet(ctx, "key", ttl); err == nil {
			t.Fatal("invalid ttl accepted")
		}
	}
	for _, endpoint := range []string{"http://example.com", "https://user@example.com", "https://example.com/path", "https://example.com?x=y"} {
		if _, err = NewR2(R2Config{endpoint, "bucket", "key", "secret"}); err == nil {
			t.Fatal("unsafe endpoint", endpoint)
		}
	}
	if _, err = NewR2(R2Config{"https://account.r2.cloudflarestorage.com", "bucket", "key", "secret"}); err != nil {
		t.Fatal(err)
	}
}
