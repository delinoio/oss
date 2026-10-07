// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/knownmodels"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (s *Service) knownSubscriptionModels() *knownmodels.Manager {
	s.knownModelsOnce.Do(func() {
		base := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: knownmodels.RequestTimeout}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: knownmodels.RequestTimeout, ResponseHeaderTimeout: knownmodels.RequestTimeout, MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true, DisableCompression: true}
		s.knownModels = knownmodels.New(s.Store.Root(), &outbound.Transport{Base: base, Resolve: s.outboundResolver()}, s.logger)
	})
	return s.knownModels
}

func (s *Service) ListKnownSubscriptionModels(ctx context.Context, req *connect.Request[pb.ListKnownSubscriptionModelsRequest]) (*connect.Response[pb.ListKnownSubscriptionModelsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	_, ok := domain.PrincipalFrom(ctx)
	if !ok {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Server authentication is required.", "Use the server token or a registered device credential."), correlation)
	}
	service := ""
	switch req.Msg.SubscriptionService {
	case pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CHATGPT:
		service = "chatgpt"
	case pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CLAUDE:
		service = "claude"
	case pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_GROK:
		service = "grok"
	default:
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Choose a supported subscription service.", "Choose ChatGPT, Claude or Grok."), correlation)
	}
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return tx.Authorize() }); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	snapshot := s.knownSubscriptionModels().List(service)
	result := &pb.ListKnownSubscriptionModelsResponse{SubscriptionService: req.Msg.SubscriptionService, CatalogVersion: snapshot.Version, UpdatedAt: snapshot.UpdatedAt, Source: pb.KnownSubscriptionModelCatalogSource(snapshot.Source)}
	for _, model := range snapshot.Models {
		result.Models = append(result.Models, &pb.KnownSubscriptionModel{NativeId: model.NativeID, DisplayName: model.DisplayName, Order: model.Order, MinimumHarnessVersion: model.MinimumHarnessVersion, RetirementDate: model.RetirementDate})
	}
	response := connect.NewResponse(result)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
