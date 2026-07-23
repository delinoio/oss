package auth

import "fmt"

// ErrorKind is a stable, safe authentication failure classification.
type ErrorKind uint8

const (
	ErrorUnknown ErrorKind = iota
	ErrorMissingToken
	ErrorMalformedToken
	ErrorSignature
	ErrorIssuer
	ErrorAudience
	ErrorExpired
	ErrorTokenType
	ErrorScope
	ErrorKeyUnavailable
)

func (k ErrorKind) String() string {
	switch k {
	case ErrorMissingToken:
		return "missing_token"
	case ErrorMalformedToken:
		return "malformed_token"
	case ErrorSignature:
		return "invalid_signature"
	case ErrorIssuer:
		return "invalid_issuer"
	case ErrorAudience:
		return "invalid_audience"
	case ErrorExpired:
		return "expired_token"
	case ErrorTokenType:
		return "invalid_token_type"
	case ErrorScope:
		return "insufficient_scope"
	case ErrorKeyUnavailable:
		return "key_unavailable"
	default:
		return "authentication_failed"
	}
}

// Error intentionally stores no token, provider response, or wrapped error.
// It is safe to return through HTTP/Connect mappings and structured logs.
type Error struct {
	Kind ErrorKind
}

func (e *Error) Error() string {
	if e == nil {
		return "authentication failed"
	}
	return fmt.Sprintf("authentication failed: %s", e.Kind)
}

func authError(kind ErrorKind) error {
	return &Error{Kind: kind}
}
