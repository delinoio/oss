package rpc

import (
	"context"
	"time"

	"connectrpc.com/connect"
	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

type BootstrapConfig struct {
	APIVersion         string
	LogtoIssuer        string
	LogtoAudience      string
	DesktopClientID    string
	IOSClientID        string
	AndroidClientID    string
	AdminClientID      string
	AdminRedirectURI   string
	PublicAssetBaseURL string
	OfficialUploads    bool
	Administration     bool
}

type BootstrapService struct {
	configuration BootstrapConfig
}

func NewBootstrapService(configuration BootstrapConfig) *BootstrapService {
	return &BootstrapService{configuration: configuration}
}

func (s *BootstrapService) GetBootstrap(ctx context.Context, _ *connect.Request[devhudv1.GetBootstrapRequest]) (*connect.Response[devhudv1.GetBootstrapResponse], error) {
	response := &devhudv1.GetBootstrapResponse{
		Metadata:              metadata(CorrelationID(ctx)),
		ProjectId:             devhudv1.ProjectId_PROJECT_ID_DEVHUD,
		ProtocolSchemaVersion: 1,
		ApiVersion:            s.configuration.APIVersion,
		LogtoIssuer:           s.configuration.LogtoIssuer,
		LogtoAudience:         s.configuration.LogtoAudience,
		LogtoClients: &devhudv1.LogtoClients{
			Desktop: s.configuration.DesktopClientID,
			Ios:     s.configuration.IOSClientID,
			Android: s.configuration.AndroidClientID,
			Admin:   s.configuration.AdminClientID,
		},
		LogtoRedirects: &devhudv1.LogtoRedirects{
			Native: "devhud://auth/callback",
			Admin:  s.configuration.AdminRedirectURI,
		},
		PublicAssetBaseUrl: s.configuration.PublicAssetBaseURL,
		Capabilities: []devhudv1.StaticCapability{
			devhudv1.StaticCapability_STATIC_CAPABILITY_SETTINGS_SYNC,
			devhudv1.StaticCapability_STATIC_CAPABILITY_ACCOUNT_RECOVERY,
		},
		UploadLimits: &devhudv1.UploadLimits{
			MaxObjectBytes:         50 * 1024 * 1024,
			MaxImagesPerSubmission: 10,
			RollingUploadBytes:     1024 * 1024 * 1024,
			RollingUploadWindow:    durationpb.New(24 * time.Hour),
			StoredBytes:            20 * 1024 * 1024 * 1024,
			SignedUrls:             120,
			SignedUrlWindow:        durationpb.New(time.Hour),
			PublicGetsPerIp:        300,
			PublicGetWindow:        durationpb.New(time.Minute),
			StagingTtl:             durationpb.New(24 * time.Hour),
			MaxImageWidth:          4096,
			MaxImageHeight:         4096,
			MaxImagePixels:         16_777_216,
		},
	}
	if s.configuration.OfficialUploads {
		response.Capabilities = append(response.Capabilities, devhudv1.StaticCapability_STATIC_CAPABILITY_OFFICIAL_UPLOADS)
	}
	if s.configuration.Administration {
		response.Capabilities = append(response.Capabilities, devhudv1.StaticCapability_STATIC_CAPABILITY_ADMINISTRATION)
	}
	return connect.NewResponse(response), nil
}
