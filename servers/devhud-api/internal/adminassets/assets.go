package adminassets

import (
	"bytes"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed dist
var embedded embed.FS

const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self' https:; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func Handler() (http.Handler, error) {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			response.Header().Set("Allow", "GET, HEAD")
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(request.URL.Path, "/admin/")
		if name == "" || (!strings.Contains(path.Base(name), ".") && !strings.HasPrefix(name, "static/")) {
			name = "index.html"
		}
		if name != path.Clean(name) || strings.HasPrefix(name, "../") {
			http.NotFound(response, request)
			return
		}
		body, err := fs.ReadFile(dist, name)
		if err != nil {
			http.NotFound(response, request)
			return
		}
		if name == "index.html" {
			response.Header().Set("Cache-Control", "no-store")
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
		} else {
			response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
				response.Header().Set("Content-Type", contentType)
			}
		}
		http.ServeContent(response, request, name, time.Time{}, bytes.NewReader(body))
	}), nil
}
