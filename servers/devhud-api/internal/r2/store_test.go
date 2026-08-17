package r2

import (
	"context"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

func TestPresignPUTUsesStandardChecksumAndOnlyContractedHeaders(t *testing.T) {
	store, err := New(context.Background(), Config{
		Endpoint: "http://127.0.0.1:9000", AccessKeyID: "access", SecretAccessKey: "secret",
		StagingBucket: "staging", PublicBucket: "public",
	})
	if err != nil {
		t.Fatal(err)
	}
	var checksum [32]byte
	for index := range checksum {
		checksum[index] = byte(index)
	}
	reservation := domain.UploadReservation{
		UploadID: "0198b123-4567-7abc-8def-012345678901", StagingID: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
		StagingGeneration: 99, SizeBytes: 1234, SHA256: checksum,
		SignedURLExpiresAt: time.Now().Add(domain.UploadSignedURLLifetime),
	}
	material, err := store.PresignPUT(context.Background(), reservation)
	if err != nil {
		t.Fatal(err)
	}
	wantChecksum := base64.StdEncoding.EncodeToString(checksum[:])
	if material.ChecksumSHA256Base64 != wantChecksum || strings.Contains(material.ChecksumSHA256Base64, "-") || strings.Contains(material.ChecksumSHA256Base64, "_") {
		t.Fatalf("checksum header = %q, want standard Base64 %q", material.ChecksumSHA256Base64, wantChecksum)
	}
	parsed, err := url.Parse(material.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(parsed.Path, "/staging/BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB/99.png") {
		t.Fatalf("signed staging path = %q", parsed.Path)
	}
	signedHeaders := parsed.Query().Get("X-Amz-SignedHeaders")
	for _, header := range []string{"content-length", "content-type", "x-amz-checksum-sha256"} {
		if !strings.Contains(signedHeaders, header) {
			t.Fatalf("signed headers %q omit %q", signedHeaders, header)
		}
	}
	if strings.Contains(signedHeaders, "x-amz-meta") {
		t.Fatalf("signed headers escape the exact CORS contract: %q", signedHeaders)
	}
}

func TestStagingCORSIsExactAndRejectsWildcards(t *testing.T) {
	valid := types.CORSRule{
		AllowedOrigins: append([]string(nil), StagingCORSOrigins...),
		AllowedMethods: []string{"PUT"},
		AllowedHeaders: []string{"Content-Type", "x-amz-checksum-sha256"},
		ExposeHeaders:  []string{"ETag"},
	}
	if err := validateCORSRule(valid); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*types.CORSRule){
		"origin wildcard": func(rule *types.CORSRule) { rule.AllowedOrigins[0] = "*" },
		"header wildcard": func(rule *types.CORSRule) { rule.AllowedHeaders[0] = "*" },
		"extra method":    func(rule *types.CORSRule) { rule.AllowedMethods = append(rule.AllowedMethods, "POST") },
		"missing etag":    func(rule *types.CORSRule) { rule.ExposeHeaders = nil },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := types.CORSRule{
				AllowedOrigins: append([]string(nil), valid.AllowedOrigins...), AllowedMethods: append([]string(nil), valid.AllowedMethods...),
				AllowedHeaders: append([]string(nil), valid.AllowedHeaders...), ExposeHeaders: append([]string(nil), valid.ExposeHeaders...),
			}
			mutate(&candidate)
			if validateCORSRule(candidate) == nil {
				t.Fatal("invalid CORS policy was accepted")
			}
		})
	}
}
