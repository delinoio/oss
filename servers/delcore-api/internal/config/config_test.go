// SPDX-License-Identifier: Apache-2.0
package config

import "testing"

func valid() Config {
	return Config{Listen: "127.0.0.1:8080", DatabaseURL: "postgres://localhost/delcore", Issuer: "https://issuer.example.test", Audience: "delcore", OperatorRole: "operator", IdentityKey: "fixture-identity-key-at-least-32-bytes", Currency: "USD", MinorUnits: 2, PaddleURL: "https://sandbox-api.paddle.com", PaddleCreditProductID: "pro_fixture", PaddleToken: "fixture", PaddleWebhookSecret: "fixture-secret-at-least-16-bytes", EmailURL: "https://api.cloudflare.com/client/v4", EmailToken: "fixture", EmailAccount: "account", EmailSender: "sender@example.test", ReturnURLs: []string{"https://consumer.example.test/return"}, TimeoutSeconds: 10, RetryLimit: 3, RetrySeconds: 10, MaxPayloadBytes: 1048576, RequestsPerMinute: 100, InvitationSeconds: 3600, UsageLatenessSeconds: 3600, ReportingGraceSeconds: 3600, DeletionGraceSeconds: 3600, RawUsageRetentionSeconds: 7200, AccountingRetentionSeconds: 14400, AuditRetentionSeconds: 14400, WebhookRetentionSeconds: 7200, BacklogAlert: 100, SettlementAlertSeconds: 3600}
}
func TestConfigValidation(t *testing.T) {
	c := valid()
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.RetryLimit = 0 }, func(c *Config) { c.ReportingGraceSeconds = 1 }, func(c *Config) { c.AccountingRetentionSeconds = 1 }, func(c *Config) { c.IdentityKey = "" }, func(c *Config) { c.ReturnURLs = []string{"https://user:pass@example.test/return"} }, func(c *Config) { c.PaddleURL = "http://external.example.test" }} {
		c = valid()
		mutate(&c)
		if c.Validate() == nil {
			t.Fatal("unsafe configuration accepted")
		}
	}
	c = valid()
	if c.AllowsReturn("https://consumer.example.test/return?attacker=1") {
		t.Fatal("redirect allowlist bypass")
	}
}
