// Package webassets serves the verified UI built by the app-owned embed task.
package webassets

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

//go:embed dist
var embedded embed.FS

const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'none'"

var hashedAsset = regexp.MustCompile(`^assets/[A-Za-z0-9_.-]+\.[0-9a-f]{8}\.(js|css)(\.LICENSE\.txt)?$`)

func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method-denied", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		// The UI uses fragment navigation. Only real bundled files are served;
		// missing RPCs/assets must never succeed with an HTML fallback.
		if name != "index.html" && !hashedAsset.MatchString(name) {
			http.NotFound(w, r)
			return
		}
		if !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}
		body, err := embedded.ReadFile("dist/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		switch path.Ext(name) {
		case ".html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		case ".js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		case ".txt":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		case ".css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
		if hashedAsset.MatchString(name) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
	})
}
