// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"
)

// RewriteUsageStream preserves event IDs, sequence numbers and unrecognized
// events. It bounds each record at 256 KiB and forwards oversized records without
// rewriting. Consent and identity are rechecked for each complete snapshot.
// The caller owns cancellation and must close the upstream reader on disable.
// Headers must be prepared before the caller writes the HTTP response headers.
func (p NativeCompatibilityPolicy) RewriteUsageStream(d NativeCompatibilityDecision, status int, headers http.Header, reader io.Reader, writer io.Writer) error {
	media, _, _ := mime.ParseMediaType(headers.Get("Content-Type"))
	encoding := headers.Get("Content-Encoding")
	if d.Action != NativeCompatibilityUpstream || d.ResponseKind != NativeCompatibilityResponseUsageStream || status != 200 || media != "text/event-stream" || (encoding != "" && encoding != "identity") || !p.trialValid() {
		_, err := io.Copy(writer, reader)
		return err
	}
	nativeRewriteHeaders(headers)
	record := make([]byte, 0, nativeUsageMaxBytes)
	chunk := make([]byte, 32<<10)
	pendingCR, lineHasBytes, passthrough, first := false, false, false, true
	emit := func(b []byte) error {
		if len(b) == 0 {
			return nil
		}
		n, err := writer.Write(b)
		if err == nil && n != len(b) {
			return io.ErrShortWrite
		}
		return err
	}
	finish := func() error {
		output := record
		if !passthrough {
			output = p.rewriteUsageRecord(record, first)
		}
		if err := emit(output); err != nil {
			return err
		}
		if f, ok := writer.(interface{ Flush() }); ok {
			f.Flush()
		}
		record = record[:0]
		passthrough = false
		lineHasBytes = false
		first = false
		return nil
	}
	appendByte := func(b byte) error {
		if len(record) == nativeUsageMaxBytes {
			if err := emit(record); err != nil {
				return err
			}
			record = record[:0]
			passthrough = true
		}
		record = append(record, b)
		return nil
	}
	for {
		n, err := reader.Read(chunk)
		for _, b := range chunk[:n] {
			if pendingCR {
				pendingCR = false
				if b == '\n' {
					if err := appendByte(b); err != nil {
						return err
					}
					blank := !lineHasBytes
					lineHasBytes = false
					if blank {
						if err := finish(); err != nil {
							return err
						}
					}
					continue
				}
				if !lineHasBytes {
					if err := finish(); err != nil {
						return err
					}
				}
				lineHasBytes = false
			}
			if err := appendByte(b); err != nil {
				return err
			}
			switch b {
			case '\r':
				pendingCR = true
			case '\n':
				blank := !lineHasBytes
				lineHasBytes = false
				if blank {
					if err := finish(); err != nil {
						return err
					}
				}
			default:
				lineHasBytes = true
			}
		}
		// Oversized events do not delay the rest of the upstream stream.
		if passthrough {
			if e := emit(record); e != nil {
				return e
			}
			record = record[:0]
		}
		if err != nil {
			if err != io.EOF {
				return err
			}
			break
		}
	}
	if pendingCR && !lineHasBytes {
		return finish()
	}
	return emit(record) // Incomplete final events stay byte-identical.
}
func (p NativeCompatibilityPolicy) rewriteUsageRecord(record []byte, first bool) []byte {
	if !utf8.Valid(record) || !p.trialValid() {
		return record
	}
	type line struct {
		text, end string
		data      bool
	}
	var lines []line
	var data []string
	event := ""
	remaining := string(record)
	for len(remaining) > 0 {
		pos := strings.IndexAny(remaining, "\r\n")
		if pos < 0 {
			lines = append(lines, line{text: remaining})
			break
		}
		width := 1
		if remaining[pos] == '\r' && pos+1 < len(remaining) && remaining[pos+1] == '\n' {
			width = 2
		}
		l := line{text: remaining[:pos], end: remaining[pos : pos+width]}
		remaining = remaining[pos+width:]
		content := l.text
		if first && len(lines) == 0 {
			content = strings.TrimPrefix(content, "\ufeff")
		}
		key, value, found := strings.Cut(content, ":")
		if !found {
			value = ""
		}
		value = strings.TrimPrefix(value, " ")
		if key == "event" {
			event = value
		}
		if key == "data" {
			l.data = true
			data = append(data, value)
		}
		lines = append(lines, l)
	}
	if event != "usage.snapshot" || len(data) == 0 {
		return record
	}
	decoder := json.NewDecoder(strings.NewReader(strings.Join(data, "\n")))
	decoder.UseNumber()
	var value map[string]any
	if decoder.Decode(&value) != nil || value == nil {
		return record
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return record
	}
	if !p.rewriteUsage(value, true) {
		return record
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return record
	}
	var out bytes.Buffer
	written := false
	for i, l := range lines {
		if !l.data {
			out.WriteString(l.text)
			out.WriteString(l.end)
			continue
		}
		if written {
			continue
		}
		written = true
		if first && i == 0 && strings.HasPrefix(l.text, "\ufeff") {
			out.WriteString("\ufeff")
		}
		out.WriteString("data: ")
		out.Write(payload)
		out.WriteString(l.end)
	}
	if out.Len() > nativeUsageMaxBytes || !p.trialValid() {
		return record
	}
	return out.Bytes()
}
