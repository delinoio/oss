package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

const apiBaseURL = "https://api.cloudflare.com/client/v4"

type Client struct {
	httpClient      *http.Client
	apiToken        string
	zoneID          string
	rateLimitRuleID string
	publicBaseURL   string
	apiBaseURL      string
}

func New(httpClient *http.Client, apiToken, zoneID, rateLimitRuleID, publicBaseURL string) *Client {
	return &Client{httpClient: httpClient, apiToken: apiToken, zoneID: zoneID, rateLimitRuleID: rateLimitRuleID, publicBaseURL: strings.TrimSuffix(publicBaseURL, "/"), apiBaseURL: apiBaseURL}
}

func (c *Client) PublicURL(publicID string) string { return c.publicBaseURL + "/" + publicID + ".png" }

func (c *Client) PurgeAndRevalidate(ctx context.Context, publicURL string) error {
	body, err := json.Marshal(map[string]any{"files": []string{publicURL}})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBaseURL+"/zones/"+url.PathEscape(c.zoneID)+"/purge_cache", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.apiToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Cloudflare cache purge returned HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Success bool `json:"success"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&envelope) != nil || !envelope.Success {
		return errors.New("Cloudflare cache purge was not successful")
	}
	get, err := http.NewRequestWithContext(ctx, http.MethodGet, publicURL, nil)
	if err != nil {
		return err
	}
	get.Header.Set("Cache-Control", "no-cache")
	get.Header.Set("Pragma", "no-cache")
	verification, err := c.httpClient.Do(get)
	if err != nil {
		return err
	}
	defer verification.Body.Close()
	prefix, readErr := io.ReadAll(io.LimitReader(verification.Body, 8))
	if readErr != nil || verification.StatusCode != http.StatusOK || verification.Header.Get("Content-Type") != "image/png" || !bytes.Equal(prefix, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return errors.New("public removal marker revalidation failed")
	}
	return nil
}

func (c *Client) ValidatePublicRateLimit(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBaseURL+"/zones/"+url.PathEscape(c.zoneID)+"/rulesets/phases/http_ratelimit/entrypoint", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.apiToken)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("Cloudflare rate-limit inspection returned HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Success bool `json:"success"`
		Result  struct {
			Rules []struct {
				ID         string `json:"id"`
				Action     string `json:"action"`
				Expression string `json:"expression"`
				RateLimit  struct {
					Characteristics   []string `json:"characteristics"`
					Period            int      `json:"period"`
					RequestsPerPeriod int      `json:"requests_per_period"`
					MitigationTimeout int      `json:"mitigation_timeout"`
				} `json:"ratelimit"`
			} `json:"rules"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&envelope); err != nil || !envelope.Success {
		return errors.New("invalid Cloudflare rate-limit response")
	}
	publicBase, err := url.Parse(c.publicBaseURL)
	if err != nil || publicBase.Host == "" {
		return errors.New("public asset base URL is invalid")
	}
	expectedHost := publicBase.Host
	expectedExpression := `(http.host eq "` + expectedHost + `" and http.request.method eq "GET")`
	for _, rule := range envelope.Result.Rules {
		if rule.ID == c.rateLimitRuleID && rule.Action == "block" && rule.Expression == expectedExpression &&
			rule.RateLimit.Period == 60 && rule.RateLimit.RequestsPerPeriod == 300 &&
			rule.RateLimit.MitigationTimeout == 60 && len(rule.RateLimit.Characteristics) == 1 &&
			rule.RateLimit.Characteristics[0] == "ip.src" {
			return nil
		}
	}
	return errors.New("Cloudflare public GET rate limit does not match 300 requests per IP per minute")
}

var _ domain.UploadCache = (*Client)(nil)
