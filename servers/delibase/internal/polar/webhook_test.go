package polar

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/reliability"
)

func TestVerifyWebhookAcceptsCurrentStandardWebhookSignature(t *testing.T) {
	t.Parallel()
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	eventID := "evt_0198a000"
	timestamp := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"type":"order.paid"}`)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(eventID + "." + timestamp + "." + string(body)))
	signature := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if !verifyWebhook(key, eventID, timestamp, signature, body, now) {
		t.Fatal("valid signature was rejected")
	}
	if verifyWebhook(key, eventID, timestamp, signature, append(body, ' '), now) {
		t.Fatal("signature accepted a changed body")
	}
	if verifyWebhook(
		key, eventID, timestamp, signature, body,
		now.Add(webhookTolerance+time.Second),
	) {
		t.Fatal("stale signature was accepted")
	}
}

func TestProjectWebhookRemovesBillingPIIAndPinsPaidCycleFields(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"type":"order.paid",
		"timestamp":"2026-07-24T12:00:00Z",
		"data":{
			"id":"order_1",
			"paid":true,
			"currency":"USD",
			"billing_reason":"subscription_cycle",
			"billing_name":"Must Not Persist",
			"billing_address":{"line1":"Must Not Persist"},
			"customer_id":"customer_1",
			"product_id":"product_1",
			"subscription_id":"subscription_1",
			"customer":{
				"id":"customer_1",
				"external_id":"0198a000-0000-7000-8000-000000000001",
				"email":"secret@example.com"
			},
			"subscription":{
				"id":"subscription_1",
				"current_period_start":"2026-07-01T00:00:00Z",
				"current_period_end":"2026-08-01T00:00:00Z"
			}
		}
	}`)
	eventType, projected, err := projectWebhook(body)
	if err != nil {
		t.Fatal(err)
	}
	if eventType != reliability.WebhookOrderPaid {
		t.Fatalf("event type = %q", eventType)
	}
	for _, forbidden := range []string{
		"Must Not Persist", "secret@example.com", "billing_name", "billing_address",
	} {
		if strings.Contains(string(projected), forbidden) {
			t.Fatalf("projection retained %q: %s", forbidden, projected)
		}
	}
	var decoded projectedWebhook
	if err := json.Unmarshal(projected, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.OrderID != "order_1" || decoded.Currency != "usd" ||
		decoded.SubscriptionID != "subscription_1" || !decoded.Paid {
		t.Fatalf("projection = %#v", decoded)
	}
}

func TestProjectRefundRejectsCentToMicrounitOverflow(t *testing.T) {
	t.Parallel()
	_, _, err := projectWebhook([]byte(`{
		"type":"refund.updated",
		"timestamp":"2026-07-24T12:00:00Z",
		"data":{
			"id":"refund_1",
			"order_id":"order_1",
			"status":"succeeded",
			"currency":"usd",
			"amount":922337203685478
		}
	}`))
	if err == nil {
		t.Fatal("overflowing refund was accepted")
	}
}
