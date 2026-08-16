package server

import (
	"net/http"
	"sort"
	"strings"
)

var allowedOrigins = map[string]struct{}{
	"http://localhost:46305": {},
	"http://127.0.0.1:46305": {},
	"http://localhost:46306": {},
	"http://127.0.0.1:46306": {},
	"http://tauri.localhost": {},
}

var allowedRequestHeaders = map[string]string{
	"authorization":            "Authorization",
	"connect-protocol-version": "Connect-Protocol-Version",
	"connect-timeout-ms":       "Connect-Timeout-Ms",
	"content-type":             "Content-Type",
}

const exposedHeaders = "Grpc-Status,Grpc-Message,Grpc-Status-Details-Bin,X-Devhud-Correlation-Id"

func cors(next http.Handler, connectPaths map[string]struct{}) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(response, request)
			return
		}
		appendVary(response.Header(), "Origin")
		if _, ok := allowedOrigins[origin]; !ok {
			http.Error(response, "origin is not allowed", http.StatusForbidden)
			return
		}
		response.Header().Set("Access-Control-Allow-Origin", origin)
		response.Header().Set("Access-Control-Expose-Headers", exposedHeaders)
		if request.Method != http.MethodOptions {
			next.ServeHTTP(response, request)
			return
		}

		appendVary(response.Header(), "Access-Control-Request-Method")
		appendVary(response.Header(), "Access-Control-Request-Headers")
		if _, ok := connectPaths[request.URL.Path]; !ok {
			http.NotFound(response, request)
			return
		}
		requestedMethod := request.Header.Get("Access-Control-Request-Method")
		if requestedMethod != http.MethodPost {
			http.Error(response, "method is not allowed", http.StatusForbidden)
			return
		}
		if !headersAllowed(request.Header.Get("Access-Control-Request-Headers")) {
			http.Error(response, "request header is not allowed", http.StatusForbidden)
			return
		}
		response.Header().Set("Access-Control-Allow-Methods", "POST,OPTIONS")
		response.Header().Set("Access-Control-Allow-Headers", "Authorization,Connect-Protocol-Version,Connect-Timeout-Ms,Content-Type")
		response.Header().Set("Access-Control-Max-Age", "7200")
		response.WriteHeader(http.StatusNoContent)
	})
}

func headersAllowed(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	for _, header := range strings.Split(raw, ",") {
		if _, ok := allowedRequestHeaders[strings.ToLower(strings.TrimSpace(header))]; !ok {
			return false
		}
	}
	return true
}

func appendVary(header http.Header, value string) {
	values := header.Values("Vary")
	for _, existing := range values {
		for _, part := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(part), value) {
				return
			}
		}
	}
	values = append(values, value)
	sort.Strings(values)
	header["Vary"] = values
}
