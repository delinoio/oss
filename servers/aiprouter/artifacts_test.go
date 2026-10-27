// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/artifacts"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"image"
	"image/png"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

type objectFixture struct {
	objects       map[string][]byte
	deleteFailure bool
}

func (o *objectFixture) Put(_ context.Context, key, contentType string, data []byte) error {
	o.objects[key] = append([]byte(nil), data...)
	return nil
}
func (o *objectFixture) Delete(_ context.Context, key string) error {
	if o.deleteFailure {
		return errors.New("fixture delete failure")
	}
	delete(o.objects, key)
	return nil
}
func (o *objectFixture) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	if ttl > 5*time.Minute {
		return "", errors.New("unbounded signed URL")
	}
	return "https://objects.example/" + key + "?X-Amz-Date=" + time.Now().UTC().Format("20060102T150405Z") + "&X-Amz-Expires=" + strconv.Itoa(int(ttl.Seconds())), nil
}
func TestArtifactBatchWithDurableOwnershipAndCleanup(t *testing.T) {
	auth, sc, raw := dispatchFixture(t)
	ctx := context.Background()
	p, release, e := auth.Admit(ctx, raw)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	objects := &objectFixture{objects: map[string][]byte{}}
	service := artifacts.New(auth.s, objects)
	adapter := artifactAdapter{service, auth}
	var encoded bytes.Buffer
	if e := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); e != nil {
		t.Fatal(e)
	}
	imageBytes := encoded.Bytes()
	urls, e := adapter.PutBatch(ctx, p, []inference.GeneratedImage{{Data: imageBytes, ContentType: "image/png"}, {Data: imageBytes, ContentType: "image/png"}})
	if e != nil {
		t.Fatal(e)
	}
	if len(urls) != 2 || len(objects.objects) != 2 {
		t.Fatal("batch not published")
	}
	signed, _ := url.Parse(urls[0])
	id := signed.Path[strings.LastIndex(signed.Path, "/")+1:]
	if e = auth.s.RevokeKey(ctx, sc, p.KeyID); e != nil {
		t.Fatal(e)
	}
	if _, e = adapter.URL(ctx, p, id); e == nil {
		t.Fatal("revoked key signed artifact")
	}
	if e = auth.s.DeleteOrganization(ctx, sc); e != nil {
		t.Fatal(e)
	}
	objects.deleteFailure = true
	if e = service.Cleanup(ctx); e == nil {
		t.Fatal("object deletion failure not surfaced")
	}
	objects.deleteFailure = false
	if e = service.Cleanup(ctx); e != nil {
		t.Fatal(e)
	}
	if len(objects.objects) != 0 {
		t.Fatal("deleted organization objects remain")
	}
}
