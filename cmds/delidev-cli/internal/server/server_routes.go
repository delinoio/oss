// SPDX-License-Identifier: Apache-2.0
package server

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func (s *Service) rpcMux() *http.ServeMux {
	mux := http.NewServeMux()
	options := []connect.HandlerOption{connect.WithReadMaxBytes(2 << 20), connect.WithSendMaxBytes(5 << 20)}
	mux.Handle(delidevv1connect.NewSystemServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewResourceServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewSearchServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewActivityServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewUsageServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewConfigurationServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewDeviceServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewWorkerServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewForwardServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewAccountServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewProviderServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewIntegrationServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewNetworkServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewSessionServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewInteractionServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewInboxServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewScheduleServiceHandler(s, options...))
	return mux
}
