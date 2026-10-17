// SPDX-License-Identifier: Apache-2.0
package provider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/servers/delcore-api/internal/config"
)

var ErrTransient = errors.New("provider transient failure")
var ErrPermanent = errors.New("provider rejected request")
var ErrAmbiguous = errors.New("payment outcome requires reconciliation")

type Client struct {
	Config config.Config
	HTTP   *http.Client
}

func New(c config.Config) *Client {
	return &Client{c, &http.Client{Timeout: c.Timeout(), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) Request(ctx context.Context, method, path string, input any) (json.RawMessage, error) {
	raw, e := json.Marshal(input)
	if e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Config.PaddleURL, "/")+path, bytes.NewReader(raw))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+c.Config.PaddleToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Paddle-Version", "1")
	resp, e := c.HTTP.Do(req)
	if e != nil {
		if method != http.MethodGet {
			return nil, ErrAmbiguous
		}
		return nil, ErrTransient
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, c.Config.MaxPayloadBytes+1))
	if e != nil || int64(len(data)) > c.Config.MaxPayloadBytes {
		return nil, ErrAmbiguous
	}
	if resp.StatusCode >= 500 {
		if method != http.MethodGet {
			return nil, ErrAmbiguous
		}
		return nil, ErrTransient
	}
	if resp.StatusCode == 429 {
		return nil, ErrTransient
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ErrPermanent
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil || len(envelope.Data) == 0 {
		return nil, ErrAmbiguous
	}
	return envelope.Data, nil
}
func (c *Client) SendEmail(ctx context.Context, to, subject, text string) error {
	body, _ := json.Marshal(map[string]any{"to": to, "from": map[string]string{"address": c.Config.EmailSender}, "subject": subject, "text": text, "html": "<p>" + html.EscapeString(text) + "</p>"})
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Config.EmailURL, "/")+"/accounts/"+url.PathEscape(c.Config.EmailAccount)+"/email/sending/send", bytes.NewReader(body))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+c.Config.EmailToken)
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return ErrTransient
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return ErrTransient
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrPermanent
	}
	var result struct {
		Success bool `json:"success"`
		Result  struct {
			Delivered []json.RawMessage `json:"delivered"`
			Queued    []json.RawMessage `json:"queued"`
			Bounces   []json.RawMessage `json:"permanent_bounces"`
		} `json:"result"`
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, c.Config.MaxPayloadBytes+1))
	if e != nil || int64(len(data)) > c.Config.MaxPayloadBytes || json.Unmarshal(data, &result) != nil {
		return ErrTransient
	}
	if !result.Success || len(result.Result.Bounces) > 0 {
		return ErrPermanent
	}
	if len(result.Result.Delivered)+len(result.Result.Queued) == 0 {
		return ErrTransient
	}
	return nil
}
func VerifySignature(secret, header string, body []byte, now time.Time) bool {
	var timestamp string
	var signatures []string
	for _, part := range strings.Split(header, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		if kv[0] == "ts" {
			if timestamp != "" {
				return false
			}
			timestamp = kv[1]
		}
		if kv[0] == "h1" {
			signatures = append(signatures, kv[1])
		}
	}
	seconds, e := strconv.ParseInt(timestamp, 10, 64)
	if e != nil {
		return false
	}
	age := now.Sub(time.Unix(seconds, 0))
	if age > 5*time.Minute || age < -30*time.Second {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + ":"))
	mac.Write(body)
	expected := mac.Sum(nil)
	for _, sig := range signatures {
		b, e := hex.DecodeString(sig)
		if e == nil && hmac.Equal(b, expected) {
			return true
		}
	}
	return false
}

type Event struct {
	ID         string          `json:"event_id"`
	Type       string          `json:"event_type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}
type Transaction struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	CustomerID     string `json:"customer_id"`
	SubscriptionID string `json:"subscription_id"`
	Currency       string `json:"currency_code"`
	Custom         struct {
		JobID    string `json:"delcore_job_id"`
		TenantID string `json:"delcore_tenant_id"`
	} `json:"custom_data"`
	Details struct {
		Totals struct {
			Subtotal string `json:"subtotal"`
			Total    string `json:"total"`
		} `json:"totals"`
	} `json:"details"`
	Checkout struct {
		URL string `json:"url"`
	} `json:"checkout"`
	BillingPeriod *struct {
		StartsAt time.Time `json:"starts_at"`
		EndsAt   time.Time `json:"ends_at"`
	} `json:"billing_period"`
}
