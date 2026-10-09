// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"net/http"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func (s *Service) rpcMux() *http.ServeMux {
	mux := http.NewServeMux()
	options := []connect.HandlerOption{connect.WithReadMaxBytes(2 << 20), connect.WithSendMaxBytes(5 << 20)}
	mux.Handle(delidevv1connect.NewMcpManagementServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewMcpWorkerServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewSkillServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewAttachmentServiceHandler(s, connect.WithReadMaxBytes(512<<10), connect.WithSendMaxBytes(512<<10)))
	mux.Handle(delidevv1connect.NewWorkspaceStorageServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewInstallationServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewSystemServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewResourceServiceHandler(s, append(options, connect.WithConditionalHandlerOptions(func(spec connect.Spec) []connect.HandlerOption {
		if spec.Procedure == delidevv1connect.ResourceServiceGetResourceProcedure {
			return []connect.HandlerOption{connect.WithSendMaxBytes(2 * domain.MaxRepositoryBranchesJobBytes)}
		}
		return nil
	}))...))
	mux.Handle(delidevv1connect.NewSearchServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewActivityServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewUsageServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewConfigurationServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewDeviceServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewWorkerServiceHandler(s, append(options, connect.WithConditionalHandlerOptions(func(spec connect.Spec) []connect.HandlerOption {
		if spec.Procedure == delidevv1connect.WorkerServiceReportWorkProcedure {
			return []connect.HandlerOption{connect.WithReadMaxBytes(domain.MaxRepositoryBranchesJobBytes), connect.WithSendMaxBytes(2 * domain.MaxRepositoryBranchesJobBytes)}
		}
		return nil
	}))...))
	mux.Handle(delidevv1connect.NewForwardServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewTerminalServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewBrowserServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewAccountServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewSubscriptionServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewProviderServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewNativeModelServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewIntegrationServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewPullRequestFixServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewNetworkServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewSessionServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewInteractionServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewInboxServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewScheduleServiceHandler(s, options...))
	return mux
}
