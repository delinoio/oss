package rpc

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
)

func TestBootstrapAdvertisesCrashReportsAsStaticCapability(t *testing.T) {
	response, err := NewBootstrapService(BootstrapConfig{}).GetBootstrap(
		WithCorrelationID(context.Background(), testCorrelationID),
		connect.NewRequest(&devhudv1.GetBootstrapRequest{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range response.Msg.GetCapabilities() {
		if capability == devhudv1.StaticCapability_STATIC_CAPABILITY_CRASH_REPORTS {
			return
		}
	}
	t.Fatal("crash reports must be a compile-time static capability")
}
