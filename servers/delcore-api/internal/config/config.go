// SPDX-License-Identifier: Apache-2.0
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

type Config struct {
	PaddleCreditProductID      string   `json:"paddle_credit_product_id"`
	Listen                     string   `json:"listen"`
	DatabaseURL                string   `json:"database_url"`
	Issuer                     string   `json:"issuer"`
	Audience                   string   `json:"audience"`
	OperatorRole               string   `json:"operator_role"`
	IdentityKey                string   `json:"identity_key"`
	Currency                   string   `json:"currency"`
	MinorUnits                 int      `json:"minor_units"`
	PaddleURL                  string   `json:"paddle_url"`
	PaddleToken                string   `json:"paddle_token"`
	PaddleWebhookSecret        string   `json:"paddle_webhook_secret"`
	EmailURL                   string   `json:"email_url"`
	EmailToken                 string   `json:"email_token"`
	EmailAccount               string   `json:"email_account"`
	EmailSender                string   `json:"email_sender"`
	ReturnURLs                 []string `json:"return_urls"`
	TimeoutSeconds             int      `json:"timeout_seconds"`
	RetryLimit                 int      `json:"retry_limit"`
	RetrySeconds               int      `json:"retry_seconds"`
	MaxPayloadBytes            int64    `json:"max_payload_bytes"`
	RequestsPerMinute          int      `json:"requests_per_minute"`
	InvitationSeconds          int      `json:"invitation_seconds"`
	UsageLatenessSeconds       int      `json:"usage_lateness_seconds"`
	ReportingGraceSeconds      int      `json:"reporting_grace_seconds"`
	DeletionGraceSeconds       int      `json:"deletion_grace_seconds"`
	RawUsageRetentionSeconds   int      `json:"raw_usage_retention_seconds"`
	AccountingRetentionSeconds int      `json:"accounting_retention_seconds"`
	AuditRetentionSeconds      int      `json:"audit_retention_seconds"`
	WebhookRetentionSeconds    int      `json:"webhook_retention_seconds"`
	BacklogAlert               int      `json:"backlog_alert"`
	SettlementAlertSeconds     int      `json:"settlement_alert_seconds"`
	Development                bool     `json:"development"`
}

func Load(path string) (Config, error) {
	var c Config
	f, e := os.Open(path)
	if e != nil {
		return c, e
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if c.Listen == "" || c.DatabaseURL == "" || c.Audience == "" || c.OperatorRole == "" || len(c.IdentityKey) < 32 || len(c.PaddleWebhookSecret) < 16 || c.PaddleCreditProductID == "" || c.PaddleToken == "" || c.EmailToken == "" || c.EmailAccount == "" || c.EmailSender == "" {
		return errors.New("required configuration or credentials missing")
	}
	if len(c.Currency) != 3 || c.Currency != strings.ToUpper(c.Currency) || c.MinorUnits < 0 || c.MinorUnits > 3 {
		return errors.New("invalid currency configuration")
	}
	for _, n := range []int{c.TimeoutSeconds, c.RetryLimit, c.RetrySeconds, c.RequestsPerMinute, c.InvitationSeconds, c.UsageLatenessSeconds, c.ReportingGraceSeconds, c.DeletionGraceSeconds, c.RawUsageRetentionSeconds, c.AccountingRetentionSeconds, c.AuditRetentionSeconds, c.WebhookRetentionSeconds, c.BacklogAlert, c.SettlementAlertSeconds} {
		if n <= 0 {
			return errors.New("all policy limits must be explicitly positive")
		}
	}
	if c.TimeoutSeconds > 120 || c.MaxPayloadBytes < 1024 || c.MaxPayloadBytes > 16<<20 || c.RetryLimit > 100 {
		return errors.New("request or retry bounds exceed safety limits")
	}
	if c.ReportingGraceSeconds < c.UsageLatenessSeconds || c.RawUsageRetentionSeconds < c.UsageLatenessSeconds || c.AccountingRetentionSeconds < c.RawUsageRetentionSeconds || c.WebhookRetentionSeconds < c.ReportingGraceSeconds {
		return errors.New("contradictory retention or reporting policies")
	}
	for _, s := range append([]string{c.Issuer, c.PaddleURL, c.EmailURL}, c.ReturnURLs...) {
		if !safeURL(s, c.Development) {
			return fmt.Errorf("invalid endpoint or return URL")
		}
	}
	if len(c.ReturnURLs) == 0 {
		return errors.New("return URL allowlist is required")
	}
	return nil
}
func safeURL(s string, dev bool) bool {
	u, e := url.Parse(s)
	return e == nil && u.Host != "" && u.User == nil && u.Fragment == "" && (u.Scheme == "https" || (dev && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")))
}
func (c Config) AllowsReturn(s string) bool {
	for _, v := range c.ReturnURLs {
		if s == v {
			return true
		}
	}
	return false
}
func (c Config) Timeout() time.Duration { return time.Duration(c.TimeoutSeconds) * time.Second }
