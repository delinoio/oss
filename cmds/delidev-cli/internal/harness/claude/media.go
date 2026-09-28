package claude

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type MediaSourceKind string
type MediaType string

const (
	Base64Media  MediaSourceKind = "base64"
	URLMedia     MediaSourceKind = "url"
	FileMedia    MediaSourceKind = "file"
	TextMedia    MediaSourceKind = "text"
	ContentMedia MediaSourceKind = "content"
	JPEGMedia    MediaType       = "image/jpeg"
	PNGMedia     MediaType       = "image/png"
	GIFMedia     MediaType       = "image/gif"
	WebPMedia    MediaType       = "image/webp"
	PDFMedia     MediaType       = "application/pdf"
	PlainMedia   MediaType       = "text/plain"
)

// NativeMediaSource preserves the original source, not downloaded/decoded
// display content. No file lookup, network request or image/PDF parser runs here.
type NativeMediaSource struct {
	Kind   MediaSourceKind
	Media  MediaType
	Data   *string              `json:"-"`
	URL    *string              `json:"-"`
	FileID *string              `json:"-"`
	Text   *string              `json:"-"`
	Blocks []NativeContentBlock `json:"-"`
}

type NativeMediaBlock struct {
	Source    NativeMediaSource
	Title     *string
	Context   *string
	Citations *bool
	// Preserve absent/null metadata, native cache directives and exact source
	// bytes. They are private data and cannot authorize caching or retrieval.
	Native json.RawMessage `json:"-"`
}

func decodeMediaBlock(raw []byte, kind ContentBlockKind) (*NativeMediaBlock, error) {
	var image struct {
		Type            ContentBlockKind `json:"type"`
		Source          json.RawMessage  `json:"source"`
		Cache           json.RawMessage  `json:"cache_control"`
		Transformations json.RawMessage  `json:"transformations"`
	}
	var document struct {
		Type      ContentBlockKind `json:"type"`
		Source    json.RawMessage  `json:"source"`
		Cache     json.RawMessage  `json:"cache_control"`
		Citations json.RawMessage  `json:"citations"`
		Title     *string          `json:"title"`
		Context   *string          `json:"context"`
	}
	var source, cache json.RawMessage
	result := &NativeMediaBlock{Native: bytes.Clone(raw)}
	if kind == ImageBlock {
		if decodeNativeObject(raw, &image) != nil {
			return nil, lifecycleUncertain()
		}
		if !absentOrNull(image.Transformations) {
			var transformations struct {
				Oversized *string `json:"oversized_image"`
			}
			if decodeNativeObject(image.Transformations, &transformations) != nil || (transformations.Oversized != nil && *transformations.Oversized != "downsize" && *transformations.Oversized != "error") {
				return nil, lifecycleUncertain()
			}
		}
		source, cache = image.Source, image.Cache
	} else if kind == DocumentBlock {
		if decodeNativeObject(raw, &document) != nil || (document.Title != nil && domain.Text(*document.Title, "native document title", 16<<10, false) != nil) || (document.Context != nil && domain.Text(*document.Context, "native document context", domain.MaxMessageText, false) != nil) {
			return nil, lifecycleUncertain()
		}
		if !absentOrNull(document.Citations) {
			var citations struct {
				Enabled *bool `json:"enabled"`
			}
			if decodeNativeObject(document.Citations, &citations) != nil {
				return nil, lifecycleUncertain()
			}
			result.Citations = citations.Enabled
		}
		result.Title, result.Context = document.Title, document.Context
		source, cache = document.Source, document.Cache
	} else {
		return nil, lifecycleUncertain()
	}
	if validateNativeCache(cache) != nil {
		return nil, lifecycleUncertain()
	}
	decoded, err := decodeMediaSource(source, kind)
	if err != nil {
		return nil, err
	}
	result.Source = decoded
	return result, nil
}

func absentOrNull(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func validateNativeCache(raw json.RawMessage) error {
	if absentOrNull(raw) {
		return nil
	}
	var cache struct {
		Type string  `json:"type"`
		TTL  *string `json:"ttl"`
	}
	if decodeNativeObject(raw, &cache) != nil || cache.Type != "ephemeral" || (cache.TTL != nil && *cache.TTL != "5m" && *cache.TTL != "1h") {
		return lifecycleUncertain()
	}
	return nil
}

func decodeMediaSource(raw json.RawMessage, block ContentBlockKind) (NativeMediaSource, error) {
	var fields map[string]json.RawMessage
	var kind MediaSourceKind
	if domain.Decode(raw, &fields) != nil || json.Unmarshal(fields["type"], &kind) != nil {
		return NativeMediaSource{}, lifecycleUncertain()
	}
	result := NativeMediaSource{Kind: kind}
	switch kind {
	case Base64Media, TextMedia:
		var source struct {
			Type  MediaSourceKind `json:"type"`
			Media MediaType       `json:"media_type"`
			Data  *string         `json:"data"`
		}
		if decodeNativeObject(raw, &source) != nil || source.Data == nil || domain.Text(*source.Data, "native media source", domain.MaxMessageText, kind == Base64Media) != nil {
			return NativeMediaSource{}, lifecycleUncertain()
		}
		if kind == Base64Media {
			if (block == ImageBlock && !slices.Contains([]MediaType{JPEGMedia, PNGMedia, GIFMedia, WebPMedia}, source.Media)) || (block == DocumentBlock && source.Media != PDFMedia) {
				return NativeMediaSource{}, lifecycleUncertain()
			}
			decoded, err := base64.StdEncoding.Strict().DecodeString(*source.Data)
			if err != nil || len(decoded) == 0 || base64.StdEncoding.EncodeToString(decoded) != *source.Data {
				return NativeMediaSource{}, lifecycleUncertain()
			}
		} else if block != DocumentBlock || source.Media != PlainMedia {
			return NativeMediaSource{}, lifecycleUncertain()
		}
		result.Media, result.Data = source.Media, source.Data
	case URLMedia, FileMedia:
		key := "url"
		if kind == FileMedia {
			key = "file_id"
		}
		var value *string
		if len(fields) != 2 || json.Unmarshal(fields[key], &value) != nil || value == nil || domain.Text(*value, "native media reference", 16<<10, true) != nil {
			return NativeMediaSource{}, lifecycleUncertain()
		}
		if kind == URLMedia {
			result.URL = value
		} else {
			result.FileID = value
		}
	case ContentMedia:
		if block != DocumentBlock || len(fields) != 2 || absentOrNull(fields["content"]) {
			return NativeMediaSource{}, lifecycleUncertain()
		}
		var text string
		if json.Unmarshal(fields["content"], &text) == nil {
			if domain.Text(text, "native document content", domain.MaxMessageText, false) != nil {
				return NativeMediaSource{}, lifecycleUncertain()
			}
			result.Text = &text
		} else {
			var blocks []json.RawMessage
			if domain.Decode(fields["content"], &blocks) != nil || blocks == nil || len(blocks) > 1024 {
				return NativeMediaSource{}, lifecycleUncertain()
			}
			result.Blocks = make([]NativeContentBlock, 0, len(blocks))
			for _, rawBlock := range blocks {
				var header struct {
					Type ContentBlockKind `json:"type"`
				}
				// Check the nonrecursive union before decoding any nested source.
				if json.Unmarshal(rawBlock, &header) != nil || (header.Type != TextBlock && header.Type != ImageBlock) {
					return NativeMediaSource{}, lifecycleUncertain()
				}
				content, err := decodeContentBlock(rawBlock)
				if err != nil {
					return NativeMediaSource{}, err
				}
				result.Blocks = append(result.Blocks, content)
			}
		}
	default:
		return NativeMediaSource{}, domain.Fail(domain.Unsupported, "The Claude Code media source needs its native extension adapter.", "Retain the original media without retrieving or replacing its source.")
	}
	return result, nil
}
