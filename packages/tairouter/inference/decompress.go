// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"github.com/klauspost/compress/zstd"
	"io"
	"net/http"
	"strings"
)

// BodyDecoder must bound its own decoder workspace; the caller separately caps output.
// Built-in zstd uses a bounded workspace and single decoder worker.
type BodyDecoder func(io.Reader) (io.ReadCloser, error)

func (h *Handler) readRequest(r *http.Request, max int64) ([]byte, error) {
	raw, e := boundedUpload(r, max)
	if e != nil {
		return nil, e
	}
	encoding := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding")))
	if encoding == "" || encoding == "identity" {
		return raw, nil
	}
	var reader io.ReadCloser
	switch encoding {
	case "zstd":
		var decoder *zstd.Decoder
		decoder, e = zstd.NewReader(bytes.NewReader(raw), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(64<<20), zstd.WithDecoderMaxWindow(32<<20))
		if e == nil {
			reader = decoder.IOReadCloser()
		}
	case "gzip":
		reader, e = gzip.NewReader(bytes.NewReader(raw))
	case "deflate":
		reader, e = zlib.NewReader(bytes.NewReader(raw))
		if e != nil {
			reader = flate.NewReader(bytes.NewReader(raw))
			e = nil
		}
	default:
		if decoder := h.c.BodyDecoders[encoding]; decoder != nil {
			reader, e = decoder(bytes.NewReader(raw))
		} else {
			return nil, &Error{415, "unsupported_content_encoding", 0}
		}
	}
	if e != nil {
		return nil, &Error{400, "invalid_compressed_body", 0}
	}
	defer reader.Close()
	out, e := io.ReadAll(io.LimitReader(reader, max+1))
	if int64(len(out)) > max {
		return nil, &Error{413, "request_too_large", 0}
	}
	if e != nil {
		return nil, &Error{400, "invalid_compressed_body", 0}
	}
	return out, nil
}
