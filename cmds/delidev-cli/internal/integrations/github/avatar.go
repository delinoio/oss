package github

import (
	"bytes"
	"context"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

func validAvatarLocator(locator, actor string) bool {
	u, err := url.Parse(locator)
	if err != nil || !domain.PositiveDecimal(actor) || u.Scheme != "https" || u.Host != "avatars.githubusercontent.com" || u.User != nil || u.Fragment != "" || u.EscapedPath() != "/u/"+actor {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	for k, v := range q {
		if len(v) != 1 {
			return false
		}
		if k == "v" && v[0] == "4" {
			continue
		}
		if k == "s" {
			n, e := strconv.Atoi(v[0])
			if e == nil && n > 0 && n <= 1024 {
				continue
			}
		}
		return false
	}
	return true
}
func (c *Client) ReadAvatar(ctx context.Context, locator, actor string) ([]byte, error) {
	if !validAvatarLocator(locator, actor) {
		return nil, queryUnavailable()
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(bounded, http.MethodGet, locator, nil)
	if err != nil {
		return nil, queryUnavailable()
	}
	// A separate no-cookie client uses the selected outbound transport but never
	// adds the GitHub PAT, product bearer or an origin Authorization header.
	client := http.Client{Transport: c.http.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, queryUnavailable()
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, queryUnavailable()
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (128<<10)+1))
	if err != nil || len(raw) > 128<<10 {
		return nil, queryUnavailable()
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "png" && format != "jpeg" && format != "webp") || config.Width < 1 || config.Height < 1 || config.Width > 1024 || config.Height > 1024 {
		return nil, queryUnavailable()
	}
	source, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, queryUnavailable()
	}
	raster := image.NewRGBA(image.Rect(0, 0, 64, 64))
	draw.ApproxBiLinear.Scale(raster, raster.Bounds(), source, source.Bounds(), draw.Src, nil)
	var out bytes.Buffer
	if png.Encode(&out, raster) != nil {
		return nil, queryUnavailable()
	}
	return out.Bytes(), nil
}
