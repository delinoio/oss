package apiproxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func safeCode(err error) domain.Code {
	code := domain.SafeError(err).Code
	switch code {
	case domain.InvalidArgument, domain.Unsupported, domain.Unauthenticated, domain.PermissionDenied, domain.ResourceExhausted, domain.Canceled, domain.Conflict, domain.NotFound, domain.RecoveryRequired:
		return code
	default:
		return domain.Unavailable
	}
}
func networkCode(err error) domain.Code {
	if errors.Is(err, context.Canceled) {
		return domain.Canceled
	}
	return domain.Unavailable
}
func providerCode(status int) domain.Code {
	switch status {
	case 400, 422:
		return domain.InvalidArgument
	case 401:
		return domain.Unauthenticated
	case 403:
		return domain.PermissionDenied
	case 404, 405, 501:
		return domain.Unsupported
	case 409:
		return domain.Conflict
	case 429:
		return domain.ResourceExhausted
	default:
		return domain.Unavailable
	}
}
func errorStatus(err error) int {
	switch safeCode(err) {
	case domain.InvalidArgument:
		return http.StatusBadRequest
	case domain.Unauthenticated, domain.Canceled:
		return http.StatusUnauthorized
	case domain.PermissionDenied:
		return http.StatusForbidden
	case domain.ResourceExhausted:
		return http.StatusTooManyRequests
	case domain.Unsupported:
		return http.StatusNotImplemented
	case domain.Conflict, domain.RecoveryRequired:
		return http.StatusConflict
	case domain.NotFound:
		return http.StatusNotFound
	default:
		return http.StatusServiceUnavailable
	}
}
func errorBody(protocol domain.APIProtocol, code domain.Code, correlation string) []byte {
	nativeType, message := "api_error", "The provider operation could not be completed. Inspect the correlated DeliDev diagnostic."
	switch code {
	case domain.Unauthenticated:
		nativeType, message = "authentication_error", "The execution credential or selected account is unavailable."
	case domain.PermissionDenied:
		nativeType, message = "permission_error", "The operation is outside this execution's authorization."
	case domain.InvalidArgument:
		nativeType, message = "invalid_request_error", "The native API request was rejected."
	case domain.Unsupported:
		nativeType, message = "invalid_request_error", "The requested native API operation or protocol is unsupported."
	case domain.ResourceExhausted:
		nativeType, message = "rate_limit_error", "The provider or proxy request limit was reached."
	case domain.Conflict, domain.RecoveryRequired:
		nativeType, message = "invalid_request_error", "The execution requires state reconciliation before this operation."
	case domain.Canceled:
		nativeType, message = "api_error", "The authorized execution request was canceled."
	}
	var value any
	if protocol == domain.AnthropicMessages {
		value = map[string]any{"type": "error", "error": map[string]any{"type": nativeType, "message": message}, "request_id": correlation}
	} else {
		value = map[string]any{"error": map[string]any{"type": nativeType, "code": string(code), "message": message, "param": nil}}
	}
	raw, _ := json.Marshal(value)
	return raw
}
func writeError(w http.ResponseWriter, status int, protocol domain.APIProtocol, code domain.Code, correlation string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(errorBody(protocol, code, correlation))
}
