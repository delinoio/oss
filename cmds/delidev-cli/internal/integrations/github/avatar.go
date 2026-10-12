// SPDX-License-Identifier: Apache-2.0
package github

import (
	"bytes"
	"context"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Avatar receives a server-retained locator and no credential parameter. The
// selected outbound route still owns proxy authentication, never origin auth.
func (c *Client) Avatar(ctx context.Context, locator, actorID string) ([]byte, error) {
	if !AvatarLocator(locator, actorID) {
		return nil, queryUnavailable()
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, e := http.NewRequestWithContext(bounded, http.MethodGet, locator, nil)
	if e != nil {
		return nil, queryUnavailable()
	}
	request.Header.Set("User-Agent", "DeliDev/0.1.0")
	request.Header.Set("Accept", "image/png,image/jpeg,image/webp")
	reply, e := c.http.Do(request)
	if e != nil {
		if bounded.Err() != nil {
			return nil, domain.SafeError(bounded.Err())
		}
		return nil, queryUnavailable()
	}
	defer reply.Body.Close()
	if reply.StatusCode != http.StatusOK {
		return nil, queryUnavailable()
	}
	raw, e := io.ReadAll(io.LimitReader(reply.Body, (128<<10)+1))
	if e != nil || len(raw) > 128<<10 {
		return nil, queryUnavailable()
	}
	config, format, e := image.DecodeConfig(bytes.NewReader(raw))
	if e != nil || (format != "png" && format != "jpeg" && format != "webp") || config.Width < 1 || config.Height < 1 || config.Width > 1024 || config.Height > 1024 {
		return nil, queryUnavailable()
	}
	decoded, decodedFormat, e := image.Decode(bytes.NewReader(raw))
	if e != nil || decodedFormat != format || decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
		return nil, queryUnavailable()
	}
	width, height := config.Width, config.Height
	if width > 64 || height > 64 {
		if width >= height {
			height = height * 64 / width
			width = 64
		} else {
			width = width * 64 / height
			height = 64
		}
		if width < 1 {
			width = 1
		}
		if height < 1 {
			height = 1
		}
	}
	sanitized := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.NearestNeighbor.Scale(sanitized, sanitized.Bounds(), decoded, decoded.Bounds(), draw.Src, nil)
	var output bytes.Buffer
	if png.Encode(&output, sanitized) != nil || output.Len() > 128<<10 {
		return nil, queryUnavailable()
	}
	return output.Bytes(), nil
}
