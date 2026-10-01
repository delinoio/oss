// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func (s *Service) Handler(origins []string, loopback bool) http.Handler {
	s.executionOnce.Do(func() { s.executionAuthority = newExecutionAuthority(s) })
	proxy := apiproxy.New(s.executionAuthority, s.logger, s.outboundResolver())
	mux := s.rpcMux()
	allowed := map[string]bool{}
	for _, origin := range origins {
		allowed[origin] = true
	}
	writer := connect.NewErrorWriter()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), writeControllerKey{}, http.NewResponseController(w)))
		correlation := string(domain.NewID())
		r.Header.Set(rpc.CorrelationHeader, correlation)
		w.Header().Set(rpc.CorrelationHeader, correlation)
		w.Header().Set("Cache-Control", "no-store")
		reject := func(err error) {
			safe := domain.SafeError(err)
			s.logger.Warn("rpc_rejected", "correlation_id", correlation, "code", safe.Code)
			_ = writer.Write(w, r, rpc.Error(err, correlation))
		}
		if loopback {
			host, _, err := net.SplitHostPort(r.Host)
			ip := net.ParseIP(host)
			if err != nil || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
				reject(domain.Fail(domain.PermissionDenied, "The RPC authority is not an allowed loopback host.", "Use the server's explicit local endpoint."))
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, apiproxy.Prefix+"/") {
			// Native execution credentials use a closed private protocol, not
			// owner/client RPC authentication or browser CORS authorization.
			proxy.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" && !allowed[origin] {
			reject(domain.Fail(domain.PermissionDenied, "The client origin is not allowed.", "Configure the exact trusted desktop origin on the server."))
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Expose-Headers", rpc.CorrelationHeader+", Connect-Content-Encoding, Connect-Accept-Encoding")
		}
		if r.Method == http.MethodOptions {
			if origin == "" || r.Header.Get("Access-Control-Request-Method") != "POST" {
				reject(domain.Fail(domain.PermissionDenied, "Invalid RPC preflight.", "Use the authenticated Connect client."))
				return
			}
			for _, name := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
				switch strings.ToLower(strings.TrimSpace(name)) {
				case "authorization", "content-type", "connect-protocol-version", "connect-timeout-ms", "connect-content-encoding", "connect-accept-encoding":
				default:
					reject(domain.Fail(domain.PermissionDenied, "An RPC preflight header is not allowed.", "Use the standard Connect headers."))
					return
				}
			}
			w.Header().Set("Access-Control-Allow-Methods", "POST")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms, Connect-Content-Encoding, Connect-Accept-Encoding")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == delidevv1connect.DeviceServicePairDeviceProcedure {
			if !s.pairingAllowed(r.RemoteAddr) {
				reject(domain.Fail(domain.ResourceExhausted, "Pairing attempts are temporarily limited.", "Wait one minute before retrying the existing grant."))
				return
			}
		} else {
			authorized, release, err := s.authorizeRequest(r)
			if err != nil {
				reject(err)
				return
			}
			defer release()
			r = authorized
		}
		started := time.Now()
		mux.ServeHTTP(w, r)
		s.logger.Debug("rpc_finished", "correlation_id", correlation, "duration_ms", time.Since(started).Milliseconds())
	})
}
