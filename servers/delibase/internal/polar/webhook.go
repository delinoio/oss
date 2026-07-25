package polar

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/contracts"
	"github.com/delinoio/oss/servers/delibase/internal/database"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/delinoio/oss/servers/internal/safeerr"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const webhookTolerance = 5 * time.Minute

type webhookIDGenerator interface {
	New() (uuid.UUID, error)
}

// NewWebhookHandler verifies the Standard Webhooks signature over the exact
// request bytes, projects the event to the PII-free billing fields delibase
// needs, and durably inserts it before acknowledging delivery.
func NewWebhookHandler(
	store *database.Store,
	secret string,
	clock contracts.Clock,
	ids webhookIDGenerator,
	logger *slog.Logger,
) (http.Handler, error) {
	key, err := webhookSecret(secret)
	if err != nil || store == nil || clock == nil || ids == nil {
		return nil, errors.New("polar: invalid webhook configuration")
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if request.Method != http.MethodPost {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, readErr := io.ReadAll(io.LimitReader(
			request.Body, maximumResponseBytes+1,
		))
		if readErr != nil || len(body) == 0 || len(body) > maximumResponseBytes {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		eventID := request.Header.Get("Webhook-Id")
		timestampHeader := request.Header.Get("Webhook-Timestamp")
		signatureHeader := request.Header.Get("Webhook-Signature")
		if !verifyWebhook(
			key, eventID, timestampHeader, signatureHeader, body, clock.Now(),
		) {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		eventType, payload, projectionErr := projectWebhook(body)
		if projectionErr != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		id, idErr := ids.New()
		if idErr != nil {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		auditID, idErr := ids.New()
		if idErr != nil {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		persistErr := store.WithinTransaction(
			request.Context(), pgx.TxOptions{},
			func(queries *dbgen.Queries) error {
				_, enqueueErr := reliability.EnqueueWebhook(
					request.Context(),
					queries,
					reliability.WebhookInput{
						ID:              id,
						Provider:        reliability.ProviderPolar,
						ProviderEventID: eventID,
						EventType:       eventType,
						Payload:         payload,
					},
				)
				if enqueueErr != nil {
					return enqueueErr
				}
				_, auditErr := reliability.AppendAudit(
					request.Context(),
					queries,
					reliability.AuditInput{
						ID:         auditID,
						OccurredAt: clock.Now().UTC(),
						EventType:  reliability.AuditWebhookReceived,
						Result:     safelog.ResultSuccess,
					},
				)
				return auditErr
			},
		)
		if errors.Is(persistErr, reliability.ErrIdempotencyConflict) {
			writer.WriteHeader(http.StatusConflict)
			return
		}
		if persistErr != nil {
			logWebhookPersistenceFailure(request.Context(), logger, persistErr)
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}), nil
}

func logWebhookPersistenceFailure(
	ctx context.Context,
	logger *slog.Logger,
	err error,
) {
	safelog.Record(ctx, logger, slog.LevelError, safelog.EventIntegration, safelog.Fields{
		Procedure:         "polar_webhook_persist",
		Result:            safelog.ResultFailure,
		ErrorClass:        safeerr.Classify(err),
		IncludeErrorClass: true,
	})
}

func verifyWebhook(
	key []byte,
	eventID string,
	timestampHeader string,
	signatureHeader string,
	body []byte,
	now time.Time,
) bool {
	if !validProviderID(eventID) {
		return false
	}
	seconds, err := strconv.ParseInt(timestampHeader, 10, 64)
	if err != nil {
		return false
	}
	signedAt := time.Unix(seconds, 0)
	difference := now.UTC().Sub(signedAt)
	if difference < 0 {
		difference = -difference
	}
	if difference > webhookTolerance {
		return false
	}
	message := eventID + "." + timestampHeader + "." + string(body)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(message))
	expected := mac.Sum(nil)
	for _, candidate := range strings.Fields(signatureHeader) {
		version, encoded, found := strings.Cut(candidate, ",")
		if !found || version != "v1" {
			continue
		}
		signature, decodeErr := base64.StdEncoding.DecodeString(encoded)
		if decodeErr != nil {
			signature, decodeErr = base64.RawStdEncoding.DecodeString(encoded)
		}
		if decodeErr == nil && hmac.Equal(signature, expected) {
			return true
		}
	}
	return false
}

func webhookSecret(value string) ([]byte, error) {
	if value == "" || value != strings.TrimSpace(value) ||
		strings.ContainsAny(value, "\x00\r\n") {
		return nil, errors.New("invalid webhook secret")
	}
	encoded := value
	for _, prefix := range []string{"polar_whs_", "whsec_"} {
		encoded = strings.TrimPrefix(encoded, prefix)
	}
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.RawURLEncoding,
	} {
		if decoded, err := encoding.DecodeString(encoded); err == nil &&
			len(decoded) >= 16 {
			return decoded, nil
		}
	}
	if len(value) < 16 {
		return nil, errors.New("invalid webhook secret")
	}
	return []byte(value), nil
}

type webhookEnvelope struct {
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

type webhookCustomer struct {
	ID         string `json:"id"`
	ExternalID string `json:"external_id"`
}

type webhookSubscription struct {
	ID                 string          `json:"id"`
	CustomerID         string          `json:"customer_id"`
	ProductID          string          `json:"product_id"`
	Status             string          `json:"status"`
	CurrentPeriodStart time.Time       `json:"current_period_start"`
	CurrentPeriodEnd   time.Time       `json:"current_period_end"`
	Customer           webhookCustomer `json:"customer"`
}

type webhookOrder struct {
	ID             string              `json:"id"`
	CustomerID     string              `json:"customer_id"`
	ProductID      string              `json:"product_id"`
	SubscriptionID string              `json:"subscription_id"`
	Currency       string              `json:"currency"`
	BillingReason  string              `json:"billing_reason"`
	Paid           bool                `json:"paid"`
	Customer       webhookCustomer     `json:"customer"`
	Subscription   webhookSubscription `json:"subscription"`
}

type webhookDispute struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Amount   int64  `json:"amount"`
	Resolved bool   `json:"resolved"`
}

type webhookRefund struct {
	ID             string          `json:"id"`
	OrderID        string          `json:"order_id"`
	SubscriptionID string          `json:"subscription_id"`
	CustomerID     string          `json:"customer_id"`
	Status         string          `json:"status"`
	Currency       string          `json:"currency"`
	Amount         int64           `json:"amount"`
	Dispute        *webhookDispute `json:"dispute"`
}

type projectedWebhook struct {
	Type               string    `json:"type"`
	EventAt            time.Time `json:"event_at"`
	ObjectID           string    `json:"object_id"`
	OrderID            string    `json:"order_id,omitempty"`
	CustomerID         string    `json:"customer_id,omitempty"`
	ExternalID         string    `json:"external_id,omitempty"`
	SubscriptionID     string    `json:"subscription_id,omitempty"`
	ProductID          string    `json:"product_id,omitempty"`
	Status             string    `json:"status,omitempty"`
	Currency           string    `json:"currency,omitempty"`
	BillingReason      string    `json:"billing_reason,omitempty"`
	Paid               bool      `json:"paid,omitempty"`
	CurrentPeriodStart time.Time `json:"current_period_start,omitempty"`
	CurrentPeriodEnd   time.Time `json:"current_period_end,omitempty"`
	AmountMicros       int64     `json:"amount_micros,omitempty"`
	Chargeback         bool      `json:"chargeback,omitempty"`
}

func projectWebhook(
	body []byte,
) (reliability.WebhookEventType, json.RawMessage, error) {
	var envelope webhookEnvelope
	if json.Unmarshal(body, &envelope) != nil || envelope.Timestamp.IsZero() {
		return "", nil, errors.New("invalid event")
	}
	eventType := reliability.WebhookEventType(envelope.Type)
	projected := projectedWebhook{Type: envelope.Type, EventAt: envelope.Timestamp.UTC()}
	switch eventType {
	case reliability.WebhookOrderPaid:
		var order webhookOrder
		if json.Unmarshal(envelope.Data, &order) != nil || !order.Paid ||
			!validProviderID(order.ID) ||
			!validProviderID(order.SubscriptionID) ||
			!validProviderID(order.CustomerID) ||
			order.Subscription.ID != order.SubscriptionID {
			return "", nil, errors.New("invalid paid order")
		}
		projected.ObjectID = order.ID
		projected.OrderID = order.ID
		projected.CustomerID = order.CustomerID
		projected.ExternalID = order.Customer.ExternalID
		projected.SubscriptionID = order.SubscriptionID
		projected.ProductID = order.ProductID
		projected.Currency = strings.ToLower(order.Currency)
		projected.BillingReason = order.BillingReason
		projected.Paid = true
		projected.CurrentPeriodStart = order.Subscription.CurrentPeriodStart.UTC()
		projected.CurrentPeriodEnd = order.Subscription.CurrentPeriodEnd.UTC()
	case reliability.WebhookSubscriptionCreated,
		reliability.WebhookSubscriptionUpdated,
		reliability.WebhookSubscriptionActive,
		reliability.WebhookSubscriptionUncanceled,
		reliability.WebhookSubscriptionPastDue,
		reliability.WebhookSubscriptionCanceled,
		reliability.WebhookSubscriptionRevoked:
		var subscription webhookSubscription
		if json.Unmarshal(envelope.Data, &subscription) != nil ||
			!validProviderID(subscription.ID) ||
			!validProviderID(subscription.CustomerID) {
			return "", nil, errors.New("invalid subscription")
		}
		projected.ObjectID = subscription.ID
		projected.CustomerID = subscription.CustomerID
		projected.ExternalID = subscription.Customer.ExternalID
		projected.SubscriptionID = subscription.ID
		projected.ProductID = subscription.ProductID
		projected.Status = subscription.Status
		projected.CurrentPeriodStart = subscription.CurrentPeriodStart.UTC()
		projected.CurrentPeriodEnd = subscription.CurrentPeriodEnd.UTC()
	case reliability.WebhookRefundCreated, reliability.WebhookRefundUpdated:
		var refund webhookRefund
		if json.Unmarshal(envelope.Data, &refund) != nil ||
			!validProviderID(refund.ID) || !validProviderID(refund.OrderID) ||
			refund.Amount < 0 || refund.Amount > math.MaxInt64/10000 {
			return "", nil, errors.New("invalid refund")
		}
		projected.ObjectID = refund.ID
		projected.OrderID = refund.OrderID
		projected.CustomerID = refund.CustomerID
		projected.SubscriptionID = refund.SubscriptionID
		projected.Status = refund.Status
		projected.Currency = strings.ToLower(refund.Currency)
		projected.AmountMicros = refund.Amount * 10000
		if refund.Dispute != nil && refund.Dispute.Resolved &&
			refund.Dispute.Status == "lost" {
			projected.Chargeback = true
			if refund.Dispute.Amount > 0 {
				if refund.Dispute.Amount > math.MaxInt64/10000 {
					return "", nil, errors.New("invalid dispute")
				}
				projected.AmountMicros = refund.Dispute.Amount * 10000
			}
		}
	default:
		return "", nil, errors.New("unsupported event")
	}
	if projected.ObjectID == "" {
		return "", nil, errors.New("missing object")
	}
	payload, err := json.Marshal(projected)
	if err != nil {
		return "", nil, err
	}
	return eventType, payload, nil
}
