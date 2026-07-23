package reliability

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/internal/safeerr"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/google/uuid"
)

func TestBackoffIsCappedExponentialWithDeterministicJitter(t *testing.T) {
	t.Parallel()
	const (
		base = time.Second
		cap  = 10 * time.Second
	)
	tests := []struct {
		name    string
		attempt int
		random  float64
		want    time.Duration
	}{
		{name: "minimum jitter", attempt: 1, random: 0, want: 500 * time.Millisecond},
		{name: "neutral jitter", attempt: 1, random: 0.5, want: time.Second},
		{name: "exponential", attempt: 4, random: 0.5, want: 8 * time.Second},
		{name: "capped", attempt: 12, random: 0.99, want: cap},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := backoff(test.attempt, base, cap, test.random); got != test.want {
				t.Fatalf("backoff() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestRegistryRejectsFreeFormAndDuplicateHandlers(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	handler := func(context.Context, Item) error { return nil }
	if err := registry.Register(HandlerPolarOrderPaid, handler); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(HandlerPolarOrderPaid, handler); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("duplicate registration error = %v", err)
	}
	if err := registry.Register(HandlerID("free-form"), handler); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("free-form registration error = %v", err)
	}
}

func TestWorkerTwelfthFailureAndDailyDeadLetterRetry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	item := Item{
		ID:           uuid.MustParse("0198a000-0000-7000-8000-000000000801"),
		Queue:        QueueWebhookInbox,
		HandlerID:    HandlerPolarOrderPaid,
		AttemptCount: MaxNormalAttempts,
		ClaimToken:   uuid.MustParse("0198a000-0000-7000-8000-000000000802"),
	}
	storage := &fakeStorage{items: []Item{item}}
	registry := NewRegistry()
	if err := registry.Register(
		HandlerPolarOrderPaid,
		func(context.Context, Item) error {
			return safeerr.New(safeerr.ClassDependency)
		},
	); err != nil {
		t.Fatal(err)
	}
	worker := testWorker(t, storage, registry, clock, &fixedRandom{value: 0.5})
	if count, err := worker.RunOnce(context.Background()); err != nil || count != 1 {
		t.Fatalf("RunOnce() = %d, %v", count, err)
	}
	if len(storage.failures) != 1 {
		t.Fatalf("failure count = %d", len(storage.failures))
	}
	first := storage.failures[0]
	if !first.deadLetter || !first.next.Equal(now.Add(DeadLetterInterval)) {
		t.Fatalf("twelfth failure = %#v", first)
	}

	clock.now = now.Add(DeadLetterInterval)
	item.DeadLetter = true
	item.DeadLetterAttemptCount = 1
	item.ClaimToken = uuid.MustParse("0198a000-0000-7000-8000-000000000803")
	storage.items = append(storage.items, item)
	if count, err := worker.RunOnce(context.Background()); err != nil || count != 1 {
		t.Fatalf("dead-letter RunOnce() = %d, %v", count, err)
	}
	second := storage.failures[1]
	if second.deadLetter || !second.next.Equal(clock.now.Add(DeadLetterInterval)) {
		t.Fatalf("daily failure = %#v", second)
	}
}

func TestWorkerRefreshesLeaseClockBeforeEachQueueClaim(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	storage := &fakeStorage{items: []Item{
		{
			ID:        uuid.MustParse("0198a000-0000-7000-8000-000000000804"),
			Queue:     QueueWebhookInbox,
			HandlerID: HandlerPolarOrderPaid,
		},
		{
			ID:        uuid.MustParse("0198a000-0000-7000-8000-000000000805"),
			Queue:     QueueIntegrationOutbox,
			HandlerID: HandlerPolarReportUsage,
		},
	}}
	registry := NewRegistry()
	if err := registry.Register(
		HandlerPolarOrderPaid,
		func(context.Context, Item) error {
			clock.now = now.Add(2 * time.Minute)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(
		HandlerPolarReportUsage,
		func(context.Context, Item) error { return nil },
	); err != nil {
		t.Fatal(err)
	}
	worker := testWorker(t, storage, registry, clock, &fixedRandom{value: 0.5})
	if count, err := worker.RunOnce(context.Background()); err != nil || count != 2 {
		t.Fatalf("RunOnce() = %d, %v", count, err)
	}
	if len(storage.claims) != 3 {
		t.Fatalf("claim count = %d", len(storage.claims))
	}
	outboxClaim := storage.claims[1]
	if !outboxClaim.claimedAt.Equal(clock.now) ||
		!outboxClaim.expiresAt.Equal(clock.now.Add(time.Minute)) {
		t.Fatalf("outbox claim lease = %#v", outboxClaim)
	}
}

func TestWorkerCompletesDeadLetterRecoveryAndDoesNotLogHandlerSecrets(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	item := Item{
		ID:                     uuid.MustParse("0198a000-0000-7000-8000-000000000811"),
		Queue:                  QueueIntegrationOutbox,
		HandlerID:              HandlerPolarReportUsage,
		EntityID:               uuid.MustParse("0198a000-0000-7000-8000-000000000812"),
		Actor:                  safelog.ActorPseudonym("actor:v1:0123456789abcdef0123456789abcdef"),
		AttemptCount:           MaxNormalAttempts,
		DeadLetterAttemptCount: 3,
		DeadLetter:             true,
		ClaimToken:             uuid.MustParse("0198a000-0000-7000-8000-000000000813"),
	}
	storage := &fakeStorage{items: []Item{item}}
	registry := NewRegistry()
	if err := registry.Register(
		HandlerPolarReportUsage,
		func(context.Context, Item) error { return nil },
	); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	worker := testWorker(t, storage, registry, clock, &fixedRandom{value: 0.5})
	worker.logger = slog.New(slog.NewJSONHandler(&output, nil))
	if count, err := worker.RunOnce(context.Background()); err != nil || count != 1 {
		t.Fatalf("RunOnce() = %d, %v", count, err)
	}
	if len(storage.completed) != 1 {
		t.Fatalf("completion count = %d", len(storage.completed))
	}
	logged := output.String()
	for _, expected := range []string{
		`"handler_id":"polar.outbox.report_usage"`,
		`"entity_id":"0198a000-0000-7000-8000-000000000812"`,
		`"actor":"actor:v1:0123456789abcdef0123456789abcdef"`,
		`"dead_letter_attempt":3`,
		`"result":"success"`,
	} {
		if !strings.Contains(logged, expected) {
			t.Fatalf("worker log missing %s: %s", expected, logged)
		}
	}
}

func TestWorkerFailureLogsOnlySafeClassification(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	item := Item{
		ID:           uuid.MustParse("0198a000-0000-7000-8000-000000000814"),
		Queue:        QueueIntegrationOutbox,
		HandlerID:    HandlerPolarReportUsage,
		EntityID:     uuid.MustParse("0198a000-0000-7000-8000-000000000815"),
		AttemptCount: 1,
		ClaimToken:   uuid.MustParse("0198a000-0000-7000-8000-000000000816"),
	}
	storage := &fakeStorage{items: []Item{item}}
	registry := NewRegistry()
	if err := registry.Register(
		HandlerPolarReportUsage,
		func(context.Context, Item) error {
			return errors.New(
				"Authorization: Bearer raw-polar-token owner@example.com 4242 4242 4242 4242",
			)
		},
	); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	worker := testWorker(
		t,
		storage,
		registry,
		&fixedClock{now: now},
		&fixedRandom{value: 0.5},
	)
	worker.logger = slog.New(slog.NewJSONHandler(&output, nil))
	if count, err := worker.RunOnce(context.Background()); err != nil || count != 1 {
		t.Fatalf("RunOnce() = %d, %v", count, err)
	}
	logged := output.String()
	for _, forbidden := range []string{
		"raw-polar-token",
		"owner@example.com",
		"4242 4242 4242 4242",
		"Authorization",
	} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("worker log leaked %q: %s", forbidden, logged)
		}
	}
	if !strings.Contains(logged, `"error_class":"internal"`) {
		t.Fatalf("worker log missing safe classification: %s", logged)
	}
}

func TestPayloadValidationRejectsCredentialsCardsAndBillingPII(t *testing.T) {
	t.Parallel()
	tests := []string{
		`{"authorization":"Bearer secret-value"}`,
		`{"nested":{"x_delibase_forwarded_user_token":"secret"}}`,
		`{"value":"eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.signature"}`,
		`{"card_number":"4242 4242 4242 4242"}`,
		`{"customer_email":"owner@example.com"}`,
		`{"billing_name":"Raw Customer"}`,
		`{"payment_method":{"id":"pm_secret"}}`,
	}
	for _, raw := range tests {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if _, err := validatePayload([]byte(raw)); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("validatePayload() error = %v", err)
			}
		})
	}
	safe, err := validatePayload([]byte(
		`{"organization_id":"0198a000-0000-7000-8000-000000000821","units":42}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if string(safe) != `{"organization_id":"0198a000-0000-7000-8000-000000000821","units":42}` {
		t.Fatalf("canonical payload = %s", safe)
	}
}

func testWorker(
	t *testing.T,
	storage Storage,
	registry *Registry,
	clock Clock,
	random Random,
) *Worker {
	t.Helper()
	worker, err := NewWorker(WorkerConfig{
		Storage:        storage,
		Registry:       registry,
		Clock:          clock,
		Random:         random,
		TokenGenerator: &fixedTokenGenerator{},
		LeaseDuration:  time.Minute,
		BaseBackoff:    time.Second,
		MaxBackoff:     time.Hour,
		PollInterval:   time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

type fixedClock struct {
	now time.Time
}

func (clock *fixedClock) Now() time.Time { return clock.now }

type fixedRandom struct {
	value float64
}

func (random *fixedRandom) Float64() float64 { return random.value }

type fixedTokenGenerator struct {
	next byte
}

func (generator *fixedTokenGenerator) New() (uuid.UUID, error) {
	generator.next++
	id := uuid.MustParse("0198a000-0000-7000-8000-000000000899")
	id[15] = generator.next
	return id, nil
}

type recordedFailure struct {
	item       Item
	failedAt   time.Time
	next       time.Time
	deadLetter bool
	class      safeerr.Class
}

type recordedClaim struct {
	queue     Queue
	claimedAt time.Time
	expiresAt time.Time
}

type fakeStorage struct {
	items     []Item
	completed []Item
	failures  []recordedFailure
	claims    []recordedClaim
	recovered int
}

func (storage *fakeStorage) RecoverExhausted(context.Context, time.Time) error {
	storage.recovered++
	return nil
}

func (storage *fakeStorage) Claim(
	_ context.Context,
	queue Queue,
	claimToken uuid.UUID,
	claimedAt time.Time,
	expiresAt time.Time,
) (Item, bool, error) {
	storage.claims = append(storage.claims, recordedClaim{
		queue:     queue,
		claimedAt: claimedAt,
		expiresAt: expiresAt,
	})
	for index, item := range storage.items {
		if item.Queue != queue {
			continue
		}
		storage.items = append(storage.items[:index], storage.items[index+1:]...)
		item.ClaimToken = claimToken
		return item, true, nil
	}
	return Item{}, false, nil
}

func (storage *fakeStorage) Complete(
	_ context.Context,
	item Item,
	_ time.Time,
) error {
	storage.completed = append(storage.completed, item)
	return nil
}

func (storage *fakeStorage) Fail(
	_ context.Context,
	item Item,
	failedAt time.Time,
	next time.Time,
	deadLetter bool,
	class safeerr.Class,
) error {
	storage.failures = append(storage.failures, recordedFailure{
		item:       item,
		failedAt:   failedAt,
		next:       next,
		deadLetter: deadLetter,
		class:      class,
	})
	return nil
}
