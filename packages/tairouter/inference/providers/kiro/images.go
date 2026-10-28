// SPDX-License-Identifier: Apache-2.0
// Image limits and age tiers adapted from opencodex; see LICENSE.upstream.
package kiro

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"strings"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

const ImageBase64Budget = 18 << 20
const MaxImagePixels = 16_000_000
const MaxImageBase64Length = 64 << 20
const imageCorrupt = "[image omitted: undecodable or corrupt image data]"
const imageBomb = "[image omitted: image too large to process safely]"
const imageOverflow = "[image omitted: total image payload exceeded the provider request budget; older images were dropped]"

type ImageTier struct {
	MaxEdge   int
	Qualities []int
	HardCap   int
}

func imageTiers() []ImageTier {
	return []ImageTier{
		{2000, []int{80, 60, 40, 30}, 2 << 20}, {1024, []int{70, 50}, 512 << 10}, {700, []int{60, 40}, 192 << 10},
		{500, []int{40}, 100 << 10}, {400, []int{30}, 100 << 10}, {320, []int{25}, int(^uint(0) >> 1)},
	}
}

// ImageCodec can extend the default PNG/JPEG/GIF/WebP/BMP/TIFF decoders. It must
// perform only in-process decoding, honor ctx, and bound its allocations. It must
// never resolve URLs, invoke processes, or perform network I/O.
type ImageCodec interface {
	Config(context.Context, []byte) (image.Config, error)
	Decode(context.Context, []byte) (image.Image, error)
}
type codecKey struct{}

func WithImageCodec(ctx context.Context, codec ImageCodec) context.Context {
	return context.WithValue(ctx, codecKey{}, codec)
}

type standardCodec struct{}

// Codec reads observe cancellation even when a decoder performs several reads
// or random-access TIFF reads. A decoder's CPU-only work cannot be preempted;
// pixel/byte caps bound that work, and the result is discarded if canceled.
type imageReader struct {
	*bytes.Reader
	ctx context.Context
}

func (r imageReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(b)
}
func (r imageReader) ReadAt(b []byte, off int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.ReadAt(b, off)
}

func (standardCodec) Config(ctx context.Context, b []byte) (image.Config, error) {
	if err := ctx.Err(); err != nil {
		return image.Config{}, err
	}
	if len(b) > MaxImageBase64Length/4*3 {
		return image.Config{}, failure(400, "kiro_image_byte_limit")
	}
	c, _, err := image.DecodeConfig(imageReader{bytes.NewReader(b), ctx})
	if ctx.Err() != nil {
		return image.Config{}, ctx.Err()
	}
	return c, err
}
func (standardCodec) Decode(ctx context.Context, b []byte) (image.Image, error) {
	// Enforce the guard at the allocation boundary as well as the pipeline's
	// first pass. Calling this codec directly must not bypass the pixel cap.
	config, err := (standardCodec{}).Config(ctx, b)
	if err != nil {
		return nil, err
	}
	if !safeDimensions(config.Width, config.Height) {
		return nil, failure(400, "kiro_image_pixel_limit")
	}
	i, _, err := image.Decode(imageReader{bytes.NewReader(b), ctx})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return i, err
}

func inlineImage(p object) (object, string) {
	u := str(p["image_url"])
	if u == "" {
		u = str(obj(p["image_url"])["url"])
	}
	mime, data := "", ""
	if source := obj(p["source"]); source != nil {
		if str(source["type"]) != "base64" {
			return nil, "[image omitted: remote image references are not supported by this provider]"
		}
		mime, data = str(source["media_type"]), str(source["data"])
	} else {
		if !strings.HasPrefix(u, "data:") {
			return nil, "[image omitted: remote image references are not supported by this provider]"
		}
		header, encoded, ok := strings.Cut(u, ",")
		if !ok || encoded == "" {
			return nil, "[image omitted: malformed inline image data URL]"
		}
		mime = strings.Split(strings.TrimPrefix(header, "data:"), ";")[0]
		if mime == "" {
			mime = "image/jpeg"
		}
		data = encoded
	}
	format := strings.ToLower(strings.TrimPrefix(mime, "image/"))
	if format == "jpg" {
		format = "jpeg"
	}
	return object{"format": format, "source": object{"bytes": data}}, ""
}

type imageEntry struct {
	carrier, value object
	carrierIndex   int
	source         string
	media          string
	key            string
	pos, size      int
	removed        bool
}

var imageSlots = make(chan struct{}, 4)

func normalizePayloadImages(ctx context.Context, payload object) error {
	return normalizePayloadImagesBudget(ctx, payload, ImageBase64Budget)
}

func normalizePayloadImagesBudget(ctx context.Context, payload object, budget int) error {
	state := obj(payload["conversationState"])
	carriers := []object{}
	for _, entry := range arr(state["history"]) {
		if m := obj(obj(entry)["userInputMessage"]); m != nil {
			carriers = append(carriers, m)
		}
	}
	if m := obj(obj(state["currentMessage"])["userInputMessage"]); m != nil {
		carriers = append(carriers, m)
	}
	entries := []*imageEntry{}
	codec, _ := ctx.Value(codecKey{}).(ImageCodec)
	if codec == nil {
		codec = standardCodec{}
	}
	for carrierIndex, carrier := range carriers {
		images := arr(carrier["images"])
		if len(images) > 20 {
			images = images[len(images)-20:]
			carrier["content"] = joinText(str(carrier["content"]), "[image omitted: exceeded the 20-image per-message cap; oldest images in this message were dropped]")
		}
		carrier["images"] = images
		for _, v := range images {
			img := obj(v)
			source := str(obj(img["source"])["bytes"])
			media := str(img["format"])
			sum := sha256.Sum256([]byte(media + ":" + source))
			entries = append(entries, &imageEntry{carrierIndex: carrierIndex, carrier: carrier, value: img, source: source, media: media, key: hex.EncodeToString(sum[:])})
		}
	}
	drop := func(e *imageEntry, note string) {
		e.removed = true
		e.carrier["content"] = joinText(str(e.carrier["content"]), note)
	}
	// Header validation runs before count admission, so invalid images cannot
	// evict valid history. Full decode still runs before pass-through.
	valid := []*imageEntry{}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(e.source) > MaxImageBase64Length {
			drop(e, imageBomb)
			continue
		}
		b, err := base64.StdEncoding.Strict().DecodeString(e.source)
		if err != nil || len(b) == 0 {
			drop(e, imageCorrupt)
			continue
		}
		config, err := codec.Config(ctx, b)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			drop(e, imageCorrupt)
			continue
		}
		if !safeDimensions(config.Width, config.Height) {
			drop(e, imageBomb)
			continue
		}
		valid = append(valid, e)
	}
	if len(valid) > 100 {
		for _, e := range valid[:len(valid)-100] {
			drop(e, "[images omitted: exceeded the 100-image request cap; oldest images in this message were dropped]")
		}
		valid = valid[len(valid)-100:]
	}
	tiers := imageTiers()
	process := func(e *imageEntry, start int) error {
		select {
		case imageSlots <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		defer func() { <-imageSlots }()
		b, err := base64.StdEncoding.Strict().DecodeString(e.source)
		if err != nil {
			drop(e, imageCorrupt)
			return nil
		}
		decoded, err := codec.Decode(ctx, b)
		if err != nil || decoded == nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			drop(e, imageCorrupt)
			return nil
		}
		bounds := decoded.Bounds()
		if !safeDimensions(bounds.Dx(), bounds.Dy()) {
			drop(e, imageBomb)
			return nil
		}
		for pos := start; pos < len(tiers); pos++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			tier := tiers[pos]
			if (e.media == "png" || e.media == "jpeg" || e.media == "gif" || e.media == "webp") && bounds.Dx() <= tier.MaxEdge && bounds.Dy() <= tier.MaxEdge && len(e.source) <= tier.HardCap {
				e.value["format"], e.value["source"] = e.media, object{"bytes": e.source}
				e.pos, e.size = pos, len(e.source)
				return nil
			}
			resized, err := resizeImage(ctx, decoded, tier.MaxEdge)
			if err != nil {
				return err
			}
			for _, quality := range tier.Qualities {
				if err := ctx.Err(); err != nil {
					return err
				}
				var encoded bytes.Buffer
				if err := jpeg.Encode(&encoded, resized, &jpeg.Options{Quality: quality}); err != nil {
					drop(e, imageCorrupt)
					return nil
				}
				data := base64.StdEncoding.EncodeToString(encoded.Bytes())
				if len(data) <= tier.HardCap {
					e.value["format"], e.value["source"] = "jpeg", object{"bytes": data}
					e.pos, e.size = pos, len(data)
					return nil
				}
			}
		}
		drop(e, imageCorrupt)
		return nil
	}
	seed, _ := ctx.Value(imagePositionsKey{}).(imagePositionSeed)
	for i, e := range valid {
		age := len(valid) - 1 - i
		pos := 2
		if age < 6 {
			pos = 0
		} else if age < 20 {
			pos = 1
		}
		if saved, ok := seed.positions[e.key]; ok {
			pos = saved
		}
		if err := process(e, pos); err != nil {
			return err
		}
	}
	total := 0
	for _, e := range valid {
		if !e.removed {
			total += e.size
		}
	}
	for _, e := range valid {
		for total > budget && !e.removed && e.pos < len(tiers)-1 {
			old := e.size
			if err := process(e, e.pos+1); err != nil {
				return err
			}
			total -= old
			if !e.removed {
				total += e.size
			}
		}
	}
	for _, e := range valid {
		if total <= budget {
			break
		}
		if !e.removed {
			total -= e.size
			drop(e, imageOverflow)
		}
	}
	if state, _ := ctx.Value(stateKey{}).(*requestState); state != nil {
		state.metadata.ImagePositions = map[string]int{}
		for _, e := range valid {
			if !e.removed {
				state.metadata.ImagePositions[e.key] = max(state.metadata.ImagePositions[e.key], e.pos)
			}
		}
	}
	for index, carrier := range carriers {
		kept := []any{}
		for _, e := range entries {
			if e.carrierIndex == index && !e.removed {
				kept = append(kept, e.value)
			}
		}
		if len(kept) == 0 {
			delete(carrier, "images")
		} else {
			carrier["images"] = kept
		}
	}

	return nil
}

func safeDimensions(w, h int) bool {
	return w > 0 && h > 0 && w <= MaxImagePixels && h <= MaxImagePixels/w
}

// Area sampling averages source pixels when shrinking. Decoded surfaces and the
// destination are independently bounded by the validated pixel/edge caps.
func resizeImage(ctx context.Context, src image.Image, edge int) (image.Image, error) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= edge && h <= edge {
		return src, nil
	}
	nw, nh := w, h
	if w >= h {
		nw = edge
		nh = max(1, h*edge/w)
	} else {
		nh = edge
		nw = max(1, w*edge/h)
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := 0; x < nw; x++ {
			x0, x1 := x*w/nw, (x+1)*w/nw
			y0, y1 := y*h/nh, (y+1)*h/nh
			var r, g, bl uint64
			var count uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					rr, gg, bb, aa := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					r += uint64(rr + 65535 - aa)
					g += uint64(gg + 65535 - aa)
					bl += uint64(bb + 65535 - aa)
					count++
				}
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(r / count >> 8), uint8(g / count >> 8), uint8(bl / count >> 8), 255})
		}
	}
	return dst, nil
}
