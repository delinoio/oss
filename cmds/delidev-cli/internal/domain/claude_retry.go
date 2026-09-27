package domain

import "strconv"

type ClaudeAPIProblem string

const (
	ClaudeAPIAuthentication ClaudeAPIProblem = "authentication_failed"
	ClaudeAPIOrganization   ClaudeAPIProblem = "oauth_org_not_allowed"
	ClaudeAPIAccountHold    ClaudeAPIProblem = "account_on_hold"
	ClaudeAPIBilling        ClaudeAPIProblem = "billing_error"
	ClaudeAPIRateLimit      ClaudeAPIProblem = "rate_limit"
	ClaudeAPIOverloaded     ClaudeAPIProblem = "overloaded"
	ClaudeAPIInvalidRequest ClaudeAPIProblem = "invalid_request"
	ClaudeAPIModelNotFound  ClaudeAPIProblem = "model_not_found"
	ClaudeAPIServerError    ClaudeAPIProblem = "server_error"
	ClaudeAPIUnknown        ClaudeAPIProblem = "unknown"
	ClaudeAPIMaxOutput      ClaudeAPIProblem = "max_output_tokens"
)

type ClaudeAPIRetryObservation struct {
	NativeEventID string              `json:"native_event_id"`
	Attempt       ClaudeProgressCount `json:"attempt"`
	MaxRetries    ClaudeProgressCount `json:"max_retries"`
	DelayMS       ClaudeProgressCount `json:"retry_delay_ms"`
	ErrorStatus   *uint16             `json:"error_status"`
	Error         ClaudeAPIProblem    `json:"error"`
}

func (r ClaudeAPIRetryObservation) Validate() error {
	if NativeIdentity(r.NativeEventID).Validate(ClaudeCode, NativeTurnIdentity) != nil || r.ErrorStatus != nil && (*r.ErrorStatus < 400 || *r.ErrorStatus > 599) {
		return invalidClaudeProgress()
	}
	for _, count := range []ClaudeProgressCount{r.Attempt, r.MaxRetries, r.DelayMS} {
		v, err := strconv.ParseUint(string(count), 10, 64)
		if err != nil || strconv.FormatUint(v, 10) != string(count) {
			return invalidClaudeProgress()
		}
	}
	switch r.Error {
	case ClaudeAPIAuthentication, ClaudeAPIOrganization, ClaudeAPIAccountHold, ClaudeAPIBilling, ClaudeAPIRateLimit, ClaudeAPIOverloaded, ClaudeAPIInvalidRequest, ClaudeAPIModelNotFound, ClaudeAPIServerError, ClaudeAPIUnknown, ClaudeAPIMaxOutput:
		return nil
	}
	return invalidClaudeProgress()
}
