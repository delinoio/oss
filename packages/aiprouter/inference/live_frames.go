// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"encoding/base64"
	"encoding/json"
	"math"
)

// dictationFrames is session-local. Invalid frames never advance its state.
type dictationFrames struct{ started, closed bool }

func onlyKeys(m object, keys ...string) bool {
	allowed := make(map[string]bool, len(keys))
	for _, k := range keys {
		allowed[k] = true
	}
	for k := range m {
		if !allowed[k] {
			return false
		}
	}
	return true
}
func frameNumber(v any, min, max float64, integer bool) bool {
	n, ok := v.(json.Number)
	f, err := n.Float64()
	ok = ok && err == nil
	return ok && !math.IsNaN(f) && !math.IsInf(f, 0) && f >= min && f <= max && (!integer || math.Trunc(f) == f)
}
func (v *dictationFrames) validate(b []byte) bool {
	if v.closed || len(b) > 64<<10 {
		return false
	}
	m, e := decode(b)
	if e != nil {
		return false
	}
	switch str(m["type"]) {
	case "session.start":
		if v.started || !onlyKeys(m, "type", "config") {
			return false
		}
		c := obj(m["config"])
		vad := obj(c["vad"])
		if !onlyKeys(c, "input_audio_format", "sample_rate_hz", "num_channels", "max_buffer_size_bytes", "max_utterance_duration_ms", "session_ttl_ms", "provider_mode", "transcript_delivery_mode", "vad") || c["input_audio_format"] != "pcm16" || !frameNumber(c["num_channels"], 1, 1, true) || !frameNumber(c["sample_rate_hz"], 8000, 192000, true) || !frameNumber(c["max_buffer_size_bytes"], 1, 4<<20, true) || !frameNumber(c["max_utterance_duration_ms"], 1, 30000, true) || !frameNumber(c["session_ttl_ms"], 1, 300000, true) {
			return false
		}
		if c["provider_mode"] != "buffered" && c["provider_mode"] != "streaming_sse" {
			return false
		}
		if c["transcript_delivery_mode"] != "final_only" && c["transcript_delivery_mode"] != "segment" && c["transcript_delivery_mode"] != "delta" {
			return false
		}
		if !onlyKeys(vad, "type", "threshold", "prefix_padding_ms", "silence_duration_ms") || vad["type"] != "server_vad" || !frameNumber(vad["threshold"], 0, 1, false) || !frameNumber(vad["prefix_padding_ms"], 0, 1000, true) || !frameNumber(vad["silence_duration_ms"], 0, 5000, true) {
			return false
		}
		v.started = true
		return true
	case "session.close":
		if !v.started || len(m) != 1 {
			return false
		}
		v.closed = true
		return true
	case "audio.append":
		if !v.started || !onlyKeys(m, "type", "audio") {
			return false
		}
		audio := str(m["audio"])
		data, err := base64.StdEncoding.Strict().DecodeString(audio)
		return err == nil && len(data) > 0 && len(data)%2 == 0 && base64.StdEncoding.EncodeToString(data) == audio
	}
	return false
}
