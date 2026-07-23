package reliability

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/delinoio/oss/servers/internal/safeerr"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
)

// Registry holds typed handlers and rejects accidental duplicate registration.
type Registry struct {
	mu       sync.RWMutex
	handlers map[HandlerID]Handler
}

type workerTransition string

const (
	transitionRecover  workerTransition = "recover"
	transitionClaim    workerTransition = "claim"
	transitionComplete workerTransition = "complete"
	transitionFail     workerTransition = "fail"
)

func NewRegistry() *Registry {
	return &Registry{handlers: make(map[HandlerID]Handler)}
}

func (registry *Registry) Register(id HandlerID, handler Handler) error {
	if registry == nil || !validHandlerID(id) || handler == nil {
		return ErrInvalidInput
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.handlers[id]; exists {
		return ErrIdempotencyConflict
	}
	registry.handlers[id] = handler
	return nil
}

func (registry *Registry) handler(id HandlerID) (Handler, bool) {
	if registry == nil {
		return nil, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	handler, ok := registry.handlers[id]
	return handler, ok
}

type WorkerConfig struct {
	Storage        Storage
	Registry       *Registry
	Clock          Clock
	Random         Random
	TokenGenerator TokenGenerator
	Logger         *slog.Logger
	LeaseDuration  time.Duration
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
	PollInterval   time.Duration
}

type Worker struct {
	storage        Storage
	registry       *Registry
	clock          Clock
	random         Random
	tokenGenerator TokenGenerator
	logger         *slog.Logger
	leaseDuration  time.Duration
	baseBackoff    time.Duration
	maxBackoff     time.Duration
	pollInterval   time.Duration
}

func NewWorker(config WorkerConfig) (*Worker, error) {
	if config.Storage == nil || config.Registry == nil ||
		config.Clock == nil || config.Random == nil ||
		config.TokenGenerator == nil || config.LeaseDuration <= 0 ||
		config.BaseBackoff <= 0 || config.MaxBackoff < config.BaseBackoff ||
		config.PollInterval <= 0 {
		return nil, ErrInvalidInput
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.DiscardHandler)
	}
	return &Worker{
		storage:        config.Storage,
		registry:       config.Registry,
		clock:          config.Clock,
		random:         config.Random,
		tokenGenerator: config.TokenGenerator,
		logger:         config.Logger,
		leaseDuration:  config.LeaseDuration,
		baseBackoff:    config.BaseBackoff,
		maxBackoff:     config.MaxBackoff,
		pollInterval:   config.PollInterval,
	}, nil
}

// RunOnce recovers expired twelfth claims and offers one due item from every
// queue to its registered handler. It returns the number of claimed items.
func (worker *Worker) RunOnce(ctx context.Context) (int, error) {
	if worker == nil {
		return 0, ErrInvalidInput
	}
	now := worker.clock.Now().UTC()
	if err := worker.storage.RecoverExhausted(ctx, now); err != nil {
		worker.record(
			ctx,
			Item{},
			transitionRecover,
			safelog.ResultFailure,
			safeerr.Classify(err),
			true,
		)
		return 0, err
	}

	processed := 0
	for _, queue := range []Queue{
		QueueWebhookInbox,
		QueueIntegrationOutbox,
		QueueDeletionJob,
	} {
		claimToken, err := worker.tokenGenerator.New()
		if err != nil {
			worker.record(
				ctx,
				Item{Queue: queue},
				transitionClaim,
				safelog.ResultFailure,
				safeerr.ClassInternal,
				true,
			)
			return processed, err
		}
		claimAt := worker.clock.Now().UTC()
		item, ok, err := worker.storage.Claim(
			ctx,
			queue,
			claimToken,
			claimAt,
			claimAt.Add(worker.leaseDuration),
		)
		if err != nil {
			worker.record(
				ctx,
				Item{Queue: queue},
				transitionClaim,
				safelog.ResultFailure,
				safeerr.Classify(err),
				true,
			)
			return processed, err
		}
		if !ok {
			continue
		}
		processed++
		worker.deliver(ctx, item)
	}
	return processed, nil
}

func (worker *Worker) deliver(ctx context.Context, item Item) {
	handler, registered := worker.registry.handler(item.HandlerID)
	if !registered {
		worker.fail(ctx, item, safeerr.ClassInternal)
		return
	}
	if err := handler(ctx, item); err != nil {
		worker.fail(ctx, item, safeerr.Classify(err))
		return
	}
	completedAt := worker.clock.Now().UTC()
	err := worker.storage.Complete(ctx, item, completedAt)
	if errors.Is(err, ErrStaleClaim) {
		worker.record(
			ctx,
			item,
			transitionComplete,
			safelog.ResultNoop,
			safeerr.ClassInternal,
			false,
		)
		return
	}
	if err != nil {
		worker.record(
			ctx,
			item,
			transitionComplete,
			safelog.ResultFailure,
			safeerr.Classify(err),
			true,
		)
		return
	}
	worker.record(
		ctx,
		item,
		transitionComplete,
		safelog.ResultSuccess,
		safeerr.ClassInternal,
		false,
	)
}

func (worker *Worker) fail(ctx context.Context, item Item, class safeerr.Class) {
	failedAt := worker.clock.Now().UTC()
	deadLetter := !item.DeadLetter && item.AttemptCount >= MaxNormalAttempts
	nextAttemptAt := failedAt.Add(DeadLetterInterval)
	if !deadLetter && !item.DeadLetter {
		nextAttemptAt = failedAt.Add(backoff(
			item.AttemptCount,
			worker.baseBackoff,
			worker.maxBackoff,
			worker.random.Float64(),
		))
	}
	err := worker.storage.Fail(
		ctx,
		item,
		failedAt,
		nextAttemptAt,
		deadLetter,
		class,
	)
	if errors.Is(err, ErrStaleClaim) {
		worker.record(ctx, item, transitionFail, safelog.ResultNoop, class, false)
		return
	}
	if err != nil {
		worker.record(
			ctx,
			item,
			transitionFail,
			safelog.ResultFailure,
			safeerr.Classify(err),
			true,
		)
		return
	}
	worker.record(ctx, item, transitionFail, safelog.ResultFailure, class, true)
}

// Run polls until cancellation. A PostgreSQL error is logged safely and retried
// on the next poll; context cancellation is the only normal exit.
func (worker *Worker) Run(ctx context.Context) error {
	if worker == nil {
		return ErrInvalidInput
	}
	ticker := time.NewTicker(worker.pollInterval)
	defer ticker.Stop()
	for {
		if _, err := worker.RunOnce(ctx); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func backoff(
	attempt int,
	base time.Duration,
	cap time.Duration,
	random float64,
) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	exponent := attempt - 1
	if exponent > 62 {
		exponent = 62
	}
	delay := float64(base) * math.Pow(2, float64(exponent))
	if delay > float64(cap) {
		delay = float64(cap)
	}
	if random < 0 {
		random = 0
	}
	if math.IsNaN(random) {
		random = 0.5
	}
	if random >= 1 {
		random = math.Nextafter(1, 0)
	}
	delay *= 0.5 + random
	if delay > float64(cap) {
		delay = float64(cap)
	}
	if delay < 1 {
		delay = 1
	}
	return time.Duration(delay)
}

func (worker *Worker) record(
	ctx context.Context,
	item Item,
	transition workerTransition,
	result safelog.Result,
	class safeerr.Class,
	includeClass bool,
) {
	attributes := []slog.Attr{
		slog.String("event", "reliability_worker"),
		slog.String("queue", item.Queue.String()),
		slog.String("transition", string(transition)),
		slog.String("result", string(result)),
	}
	if validHandlerID(item.HandlerID) {
		attributes = append(attributes, slog.String("handler_id", string(item.HandlerID)))
	}
	if item.ID != uuid.Nil {
		attributes = append(attributes, slog.String("event_id", item.ID.String()))
	}
	if item.EntityID != uuid.Nil {
		attributes = append(attributes, slog.String("entity_id", item.EntityID.String()))
	}
	if item.Actor != "" && validActor(string(item.Actor)) {
		attributes = append(attributes, slog.String("actor", string(item.Actor)))
	}
	if item.AttemptCount > 0 {
		attributes = append(attributes, slog.Int("attempt", item.AttemptCount))
	}
	if item.DeadLetter || item.DeadLetterAttemptCount > 0 {
		attributes = append(
			attributes,
			slog.Bool("dead_letter", true),
			slog.Int("dead_letter_attempt", item.DeadLetterAttemptCount),
		)
	}
	if includeClass {
		attributes = append(attributes, slog.String("error_class", class.String()))
	}
	worker.logger.LogAttrs(ctx, slog.LevelInfo, "reliability worker transition", attributes...)
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

type SystemRandom struct{}

func (SystemRandom) Float64() float64 { return rand.Float64() }

type UUIDv7TokenGenerator struct{}

func (UUIDv7TokenGenerator) New() (uuid.UUID, error) {
	return uuidv7.New()
}
