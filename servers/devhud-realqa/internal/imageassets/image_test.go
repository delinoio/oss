package imageassets

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"

	"github.com/HugoSmits86/nativewebp"
)

func TestVerifyReencodesPNGAndStripsAncillaryMetadata(t *testing.T) {
	t.Parallel()
	source := pngFixture(t)
	source = insertPNGChunk(t, source, "tEXt", []byte("secret=must-not-survive"))
	declaration := declarationFor(source, MediaTypePNG, 4, 3)
	verified, err := Verify(declaration, bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	if verified.MediaType != MediaTypePNG || verified.Width != 4 ||
		verified.Height != 3 || bytes.Contains(verified.Body, []byte("secret")) ||
		bytes.Contains(verified.Body, []byte("tEXt")) {
		t.Fatalf("sanitized PNG retained metadata: %#v", verified)
	}
	if bytes.Equal(source, verified.Body) {
		t.Fatal("verification retained the uploaded representation")
	}
	decoded, err := png.Decode(bytes.NewReader(verified.Body))
	if err != nil || decoded.Bounds() != image.Rect(0, 0, 4, 3) {
		t.Fatalf("sanitized PNG decode = %v, %v", decoded, err)
	}
}

func TestVerifyReencodesWebPAndRejectsMismatchMalformedAndBomb(t *testing.T) {
	t.Parallel()
	raster := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	raster.Set(1, 1, color.NRGBA{R: 40, G: 90, B: 140, A: 180})
	var encoded bytes.Buffer
	if err := nativewebp.Encode(&encoded, raster, nil); err != nil {
		t.Fatal(err)
	}
	source := encoded.Bytes()
	verified, err := Verify(
		declarationFor(source, MediaTypeWebP, 3, 2), bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	if verified.MediaType != MediaTypeWebP ||
		sniff(verified.Body) != MediaTypeWebP {
		t.Fatalf("verified WebP = %#v", verified)
	}

	mismatched := declarationFor(source, MediaTypePNG, 3, 2)
	if _, err = Verify(mismatched, bytes.NewReader(source)); !errors.Is(err, ErrMediaType) {
		t.Fatalf("media mismatch error = %v", err)
	}
	truncated := source[:len(source)/2]
	if _, err = Verify(
		declarationFor(truncated, MediaTypeWebP, 3, 2),
		bytes.NewReader(truncated)); !errors.Is(err, ErrMalformed) {
		t.Fatalf("malformed error = %v", err)
	}

	bomb := pngFixture(t)
	binary.BigEndian.PutUint32(bomb[16:20], 10_001)
	binary.BigEndian.PutUint32(bomb[20:24], 10_000)
	binary.BigEndian.PutUint32(
		bomb[29:33], crc32.ChecksumIEEE(bomb[12:29]))
	if _, err = Verify(
		declarationFor(bomb, MediaTypePNG, 10_001, 10_000),
		bytes.NewReader(bomb)); !errors.Is(err, ErrDecodedTooLarge) {
		t.Fatalf("bomb error = %v", err)
	}
}

func TestVerifyRejectsEncodedAndSubmissionLimits(t *testing.T) {
	t.Parallel()
	source := pngFixture(t)
	declaration := declarationFor(source, MediaTypePNG, 4, 3)
	declaration.EncodedBytes++
	if _, err := Verify(declaration, bytes.NewReader(source)); !errors.Is(err, ErrEncodedTooLarge) {
		t.Fatalf("length mismatch error = %v", err)
	}
	if err := ValidateSubmissionTotal(
		MaxSubmissionEncodedBytes-1, 2); !errors.Is(err, ErrEncodedTooLarge) {
		t.Fatalf("aggregate limit error = %v", err)
	}
	if err := ValidateSubmissionTotal(
		MaxSubmissionEncodedBytes-1, 1); err != nil {
		t.Fatalf("aggregate boundary error = %v", err)
	}
}

func TestVerifyPreservesSourceReadFailures(t *testing.T) {
	t.Parallel()
	source := pngFixture(t)
	declaration := declarationFor(source, MediaTypePNG, 4, 3)
	if _, err := Verify(
		declaration, io.MultiReader(bytes.NewReader(source[:1]), failingReader{}),
	); !errors.Is(err, ErrSourceRead) || errors.Is(err, ErrMalformed) {
		t.Fatalf("source read error = %v", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("fixture stream failure")
}

func pngFixture(t *testing.T) []byte {
	t.Helper()
	raster := image.NewNRGBA(image.Rect(0, 0, 4, 3))
	for y := range 3 {
		for x := range 4 {
			raster.Set(x, y, color.NRGBA{
				R: uint8(x * 40), G: uint8(y * 50), B: 120, A: 255,
			})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, raster); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func declarationFor(
	body []byte,
	mediaType MediaType,
	width int,
	height int,
) Declaration {
	sum := sha256.Sum256(body)
	return Declaration{
		MediaType: mediaType, EncodedBytes: int64(len(body)),
		Width: width, Height: height, SHA256: hex.EncodeToString(sum[:]),
	}
}

func insertPNGChunk(
	t *testing.T,
	source []byte,
	kind string,
	data []byte,
) []byte {
	t.Helper()
	if len(kind) != 4 || len(source) < 12 ||
		string(source[len(source)-8:len(source)-4]) != "IEND" {
		t.Fatal("invalid PNG fixture")
	}
	var chunk bytes.Buffer
	_ = binary.Write(&chunk, binary.BigEndian, uint32(len(data)))
	chunk.WriteString(kind)
	chunk.Write(data)
	checksum := crc32.NewIEEE()
	_, _ = checksum.Write([]byte(kind))
	_, _ = checksum.Write(data)
	_ = binary.Write(&chunk, binary.BigEndian, checksum.Sum32())
	result := append([]byte(nil), source[:len(source)-12]...)
	result = append(result, chunk.Bytes()...)
	result = append(result, source[len(source)-12:]...)
	return result
}
