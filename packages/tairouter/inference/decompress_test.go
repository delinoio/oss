// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"github.com/klauspost/compress/zstd"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompressedBodyBounds(t *testing.T) {
	for _, encoding := range []string{"gzip", "deflate", "raw-deflate", "zstd"} {
		var b bytes.Buffer
		switch encoding {
		case "zstd":
			w, _ := zstd.NewWriter(&b)
			w.Write([]byte(strings.Repeat("x", 2048)))
			w.Close()
		case "gzip":
			w := gzip.NewWriter(&b)
			w.Write([]byte(strings.Repeat("x", 2048)))
			w.Close()
		case "deflate":
			w := zlib.NewWriter(&b)
			w.Write([]byte(strings.Repeat("x", 2048)))
			w.Close()
		case "raw-deflate":
			w, _ := flate.NewWriter(&b, flate.DefaultCompression)
			w.Write([]byte(strings.Repeat("x", 2048)))
			w.Close()
		}
		r := httptest.NewRequest("POST", "/", bytes.NewReader(b.Bytes()))
		enc := encoding
		if enc == "raw-deflate" {
			enc = "deflate"
		}
		r.Header.Set("Content-Encoding", enc)
		h := &Handler{}
		if _, e := h.readRequest(r, 1024); publicError(e).Status != 413 {
			t.Fatalf("%s %v", encoding, e)
		}
		r = httptest.NewRequest("POST", "/", bytes.NewReader(b.Bytes()))
		r.Header.Set("Content-Encoding", enc)
		out, e := h.readRequest(r, 4096)
		if e != nil || len(out) != 2048 {
			t.Fatalf("%s %v %d", encoding, e, len(out))
		}
	}
}
