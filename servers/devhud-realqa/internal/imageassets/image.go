// Package imageassets implements RealQA's private image-transfer and public
// delivery boundary. It deliberately exposes no object-listing operation.
package imageassets

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/draw"
	"image/png"
	"io"
	"math"
	"strings"

	"github.com/HugoSmits86/nativewebp"
)

const (
	MaxImageEncodedBytes      int64 = 25 * 1024 * 1024
	MaxSubmissionEncodedBytes int64 = 250 * 1024 * 1024
	MaxDecodedPixels          int64 = 100_000_000
)

type MediaType string

const (
	MediaTypePNG  MediaType = "image/png"
	MediaTypeWebP MediaType = "image/webp"
)

var (
	ErrMalformed       = errors.New("realqa images: malformed image")
	ErrMediaType       = errors.New("realqa images: media type mismatch")
	ErrEncodedTooLarge = errors.New("realqa images: encoded image is too large")
	ErrDecodedTooLarge = errors.New("realqa images: decoded image is too large")
	ErrChecksum        = errors.New("realqa images: checksum mismatch")
	ErrDimensions      = errors.New("realqa images: dimensions mismatch")
)

type Declaration struct {
	MediaType    MediaType
	EncodedBytes int64
	Width        int
	Height       int
	SHA256       string
}

type Verified struct {
	MediaType    MediaType
	EncodedBytes int64
	Width        int
	Height       int
	SHA256       string
	Body         []byte
}

// Verify reads an independently stored upload, verifies its declared encoded
// representation, decodes it under the pixel ceiling, and writes a fresh
// metadata-free representation. The result is decoded once more before it can
// be persisted as verified.
func Verify(declaration Declaration, source io.Reader) (Verified, error) {
	if source == nil || declaration.EncodedBytes <= 0 ||
		declaration.EncodedBytes > MaxImageEncodedBytes {
		return Verified{}, ErrEncodedTooLarge
	}
	if declaration.Width <= 0 || declaration.Height <= 0 {
		return Verified{}, ErrDimensions
	}
	if err := validatePixels(declaration.Width, declaration.Height); err != nil {
		return Verified{}, err
	}
	expectedChecksum, err := decodeChecksum(declaration.SHA256)
	if err != nil {
		return Verified{}, ErrChecksum
	}

	body, err := io.ReadAll(io.LimitReader(source, MaxImageEncodedBytes+1))
	if err != nil {
		return Verified{}, ErrMalformed
	}
	if int64(len(body)) > MaxImageEncodedBytes ||
		int64(len(body)) != declaration.EncodedBytes {
		return Verified{}, ErrEncodedTooLarge
	}
	actualChecksum := sha256.Sum256(body)
	if !bytes.Equal(actualChecksum[:], expectedChecksum) {
		return Verified{}, ErrChecksum
	}
	if sniff(body) != declaration.MediaType {
		return Verified{}, ErrMediaType
	}

	config, err := decodeConfig(declaration.MediaType, body)
	if err != nil {
		return Verified{}, ErrMalformed
	}
	if config.Width != declaration.Width || config.Height != declaration.Height {
		return Verified{}, ErrDimensions
	}
	if err = validatePixels(config.Width, config.Height); err != nil {
		return Verified{}, err
	}
	decoded, err := decode(declaration.MediaType, body)
	if err != nil {
		return Verified{}, ErrMalformed
	}
	bounds := decoded.Bounds()
	if bounds.Dx() != declaration.Width || bounds.Dy() != declaration.Height {
		return Verified{}, ErrDimensions
	}

	// Copy only pixel values into a new origin-normalized raster. Ancillary PNG
	// chunks and WebP EXIF/XMP/ICC/animation chunks cannot survive this step.
	flattened := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(flattened, flattened.Bounds(), decoded, bounds.Min, draw.Src)
	var sanitized bytes.Buffer
	switch declaration.MediaType {
	case MediaTypePNG:
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		err = encoder.Encode(&sanitized, flattened)
	case MediaTypeWebP:
		err = nativewebp.Encode(&sanitized, flattened, &nativewebp.Options{
			UseExtendedFormat: true,
		})
	default:
		return Verified{}, ErrMediaType
	}
	if err != nil {
		return Verified{}, ErrMalformed
	}
	if sanitized.Len() <= 0 || int64(sanitized.Len()) > MaxImageEncodedBytes {
		return Verified{}, ErrEncodedTooLarge
	}
	sanitizedBody := sanitized.Bytes()
	if sniff(sanitizedBody) != declaration.MediaType {
		return Verified{}, ErrMediaType
	}
	sanitizedConfig, err := decodeConfig(declaration.MediaType, sanitizedBody)
	if err != nil || sanitizedConfig.Width != declaration.Width ||
		sanitizedConfig.Height != declaration.Height {
		return Verified{}, ErrMalformed
	}
	sanitizedChecksum := sha256.Sum256(sanitizedBody)
	return Verified{
		MediaType: declaration.MediaType, EncodedBytes: int64(len(sanitizedBody)),
		Width: declaration.Width, Height: declaration.Height,
		SHA256: hex.EncodeToString(sanitizedChecksum[:]),
		Body:   append([]byte(nil), sanitizedBody...),
	}, nil
}

func ValidateSubmissionTotal(current, addition int64) error {
	if current < 0 || addition < 0 || current > math.MaxInt64-addition ||
		current+addition > MaxSubmissionEncodedBytes {
		return ErrEncodedTooLarge
	}
	return nil
}

func validatePixels(width, height int) error {
	if width <= 0 || height <= 0 ||
		int64(width) > math.MaxInt64/int64(height) ||
		int64(width)*int64(height) > MaxDecodedPixels {
		return ErrDecodedTooLarge
	}
	return nil
}

func decodeChecksum(value string) ([]byte, error) {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return nil, ErrChecksum
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return nil, ErrChecksum
	}
	return decoded, nil
}

func sniff(body []byte) MediaType {
	if len(body) >= 8 && bytes.Equal(body[:8], []byte{
		0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
	}) {
		return MediaTypePNG
	}
	if len(body) >= 12 && string(body[:4]) == "RIFF" &&
		string(body[8:12]) == "WEBP" {
		return MediaTypeWebP
	}
	return ""
}

func decodeConfig(mediaType MediaType, body []byte) (image.Config, error) {
	switch mediaType {
	case MediaTypePNG:
		return png.DecodeConfig(bytes.NewReader(body))
	case MediaTypeWebP:
		return nativewebp.DecodeConfig(bytes.NewReader(body))
	default:
		return image.Config{}, ErrMediaType
	}
}

func decode(mediaType MediaType, body []byte) (image.Image, error) {
	switch mediaType {
	case MediaTypePNG:
		return png.Decode(bytes.NewReader(body))
	case MediaTypeWebP:
		return nativewebp.Decode(bytes.NewReader(body))
	default:
		return nil, ErrMediaType
	}
}
