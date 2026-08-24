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

func TestBootstrapAdvertisesOnlyTheExactOfficialUploadOrigin(t *testing.T) {
	response, err := NewBootstrapService(BootstrapConfig{
		OfficialUploads: true,
		R2Endpoint:      "https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com/private/path",
	}).GetBootstrap(WithCorrelationID(context.Background(), testCorrelationID), connect.NewRequest(&devhudv1.GetBootstrapRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := response.Msg.GetProtocolSchemaVersion(), uint32(2); got != want {
		t.Fatalf("protocol schema version = %d, want %d", got, want)
	}
	if got, want := response.Msg.GetOfficialUploadOrigin(), "https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com"; got != want {
		t.Fatalf("official upload origin = %q, want %q", got, want)
	}
}

func TestCanonicalUploadOriginRejectsAuthorityConfusion(t *testing.T) {
	for name, test := range map[string]struct {
		value string
		want  string
	}{
		"https":          {value: "https://upload.example/private", want: "https://upload.example"},
		"loopback http":  {value: "http://[::1]:9000/private", want: "http://[::1]:9000"},
		"external http":  {value: "http://upload.example/private"},
		"userinfo":       {value: "https://user:password@upload.example"},
		"query":          {value: "https://upload.example?next=https://attacker.invalid"},
		"fragment":       {value: "https://upload.example#attacker"},
		"missing scheme": {value: "//upload.example/private"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := canonicalUploadOrigin(test.value); got != test.want {
				t.Fatalf("canonicalUploadOrigin(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}
