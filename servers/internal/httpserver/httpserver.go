// Package httpserver provides conservative HTTP server, timeout, and CORS
// defaults for repository servers.
package httpserver

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/requestmeta"
)

const DeliDevOrigin = "https://deli.dev"

// Defaults are suitable baseline limits, not a production SLO contract.
type Defaults struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	HandlerTimeout    time.Duration
	MaxHeaderBytes    int
}

// DefaultTimeouts returns the repository-shared HTTP baseline.
func DefaultTimeouts() Defaults {
	return Defaults{
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		HandlerTimeout:    30 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

// Server applies the baseline transport and handler timeouts to an http.Server.
func Server(address string, handler http.Handler, defaults Defaults) *http.Server {
	defaults = defaults.withBaseline()
	if handler == nil {
		handler = http.DefaultServeMux
	}
	return &http.Server{
		Addr:              address,
		Handler:           Timeout(handler, defaults.HandlerTimeout),
		ReadHeaderTimeout: defaults.ReadHeaderTimeout,
		ReadTimeout:       defaults.ReadTimeout,
		WriteTimeout:      defaults.WriteTimeout,
		IdleTimeout:       defaults.IdleTimeout,
		MaxHeaderBytes:    defaults.MaxHeaderBytes,
	}
}

func (defaults Defaults) withBaseline() Defaults {
	baseline := DefaultTimeouts()
	if defaults.ReadHeaderTimeout <= 0 {
		defaults.ReadHeaderTimeout = baseline.ReadHeaderTimeout
	}
	if defaults.ReadTimeout <= 0 {
		defaults.ReadTimeout = baseline.ReadTimeout
	}
	if defaults.WriteTimeout <= 0 {
		defaults.WriteTimeout = baseline.WriteTimeout
	}
	if defaults.IdleTimeout <= 0 {
		defaults.IdleTimeout = baseline.IdleTimeout
	}
	if defaults.HandlerTimeout <= 0 {
		defaults.HandlerTimeout = baseline.HandlerTimeout
	}
	if defaults.MaxHeaderBytes <= 0 {
		defaults.MaxHeaderBytes = baseline.MaxHeaderBytes
	}
	return defaults
}

// Timeout bounds handler execution with a credential-free response.
func Timeout(next http.Handler, duration time.Duration) http.Handler {
	if duration <= 0 {
		duration = DefaultTimeouts().HandlerTimeout
	}
	return http.TimeoutHandler(
		next,
		duration,
		`{"error":{"class":"timeout","message":"request timed out"}}`+"\n",
	)
}

// CORSConfig defines exact browser origins and preflight behavior.
type CORSConfig struct {
	AllowedOrigins []string
	AllowedMethods []string
	AllowedHeaders []string
	ExposedHeaders []string
	MaxAge         time.Duration
}

// DefaultCORSConfig allows only the canonical DeliDev browser origin.
func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowedOrigins: []string{DeliDevOrigin},
		AllowedMethods: []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowedHeaders: []string{
			"Authorization",
			"Content-Type",
			"Connect-Protocol-Version",
			"Connect-Timeout-Ms",
			"X-User-Agent",
			auth.ForwardedUserTokenHeader,
			requestmeta.RequestIDHeader,
			requestmeta.TraceIDHeader,
			requestmeta.TraceparentHeader,
		},
		ExposedHeaders: []string{
			requestmeta.RequestIDHeader,
			requestmeta.TraceIDHeader,
		},
		MaxAge: 10 * time.Minute,
	}
}

// CORS returns strict exact-origin middleware. Wildcard origins and
// credentialed cookies are intentionally unsupported.
func CORS(config CORSConfig) (func(http.Handler) http.Handler, error) {
	if len(config.AllowedOrigins) == 0 {
		return nil, errors.New("httpserver: at least one CORS origin is required")
	}
	for _, origin := range config.AllowedOrigins {
		parsed, err := url.Parse(origin)
		if err != nil || origin == "*" || parsed.Scheme != "https" || parsed.Host == "" ||
			parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
			strings.ContainsAny(origin, "\r\n") {
			return nil, errors.New("httpserver: CORS origins must be exact HTTPS origins")
		}
	}
	if len(config.AllowedMethods) == 0 {
		config.AllowedMethods = DefaultCORSConfig().AllowedMethods
	}
	if len(config.AllowedHeaders) == 0 {
		config.AllowedHeaders = DefaultCORSConfig().AllowedHeaders
	}
	if len(config.ExposedHeaders) == 0 {
		config.ExposedHeaders = DefaultCORSConfig().ExposedHeaders
	}
	if config.MaxAge <= 0 {
		config.MaxAge = DefaultCORSConfig().MaxAge
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			origin := request.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(writer, request)
				return
			}
			writer.Header().Add("Vary", "Origin")
			if !slices.Contains(config.AllowedOrigins, origin) {
				if request.Method == http.MethodOptions {
					http.Error(writer, "origin not allowed", http.StatusForbidden)
					return
				}
				next.ServeHTTP(writer, request)
				return
			}

			writer.Header().Set("Access-Control-Allow-Origin", origin)
			writer.Header().Set("Access-Control-Expose-Headers", strings.Join(config.ExposedHeaders, ", "))
			if request.Method != http.MethodOptions {
				next.ServeHTTP(writer, request)
				return
			}
			if !slices.Contains(config.AllowedMethods, request.Header.Get("Access-Control-Request-Method")) {
				http.Error(writer, "method not allowed", http.StatusForbidden)
				return
			}
			writer.Header().Set("Access-Control-Allow-Methods", strings.Join(config.AllowedMethods, ", "))
			writer.Header().Set("Access-Control-Allow-Headers", strings.Join(config.AllowedHeaders, ", "))
			writer.Header().Set("Access-Control-Max-Age", strconv.Itoa(int(config.MaxAge/time.Second)))
			writer.WriteHeader(http.StatusNoContent)
		})
	}, nil
}
