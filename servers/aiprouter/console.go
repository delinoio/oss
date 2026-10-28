// SPDX-License-Identifier: Apache-2.0
package main

import (
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
)

var consoleRoutes = map[string]bool{"/sign-in": true, "/onboarding": true, "/overview": true, "/providers": true, "/pools": true, "/routing": true, "/keys": true, "/usage": true, "/organization-usage": true, "/connect": true, "/device": true, "/members": true, "/invitation": true, "/invitations": true, "/ownership": true, "/audit": true, "/settings": true, "/account": true, "/instance/settings": true, "/instance/retention": true, "/instance/destinations": true, "/instance/health": true, "/instance/prices": true, "/instance/audit": true}

func knownConsoleRoute(value string) bool { return consoleRoutes[value] }
func knownConsoleURL(u *url.URL) bool {
	if !knownConsoleRoute(u.Path) {
		return false
	}
	query := u.Query()
	if query.Has("page") || query.Has("org") {
		return false
	}
	switch u.Path {
	case "/device", "/invitation", "/ownership", "/sign-in":
		return true
	default:
		return !query.Has("organization_id")
	}
}

func safeReturnTo(raw string) string {
	if len(raw) > 4096 || strings.ContainsAny(raw, "\\\r\n\x00") {
		return "/overview"
	}
	u, e := url.Parse(raw)
	if e != nil || u.IsAbs() || u.Host != "" || u.User != nil || strings.HasPrefix(raw, "//") || !knownConsoleURL(u) {
		return "/overview"
	}
	return u.String()
}
func consoleHandler(directory string) (http.Handler, error) {
	root, e := os.OpenRoot(directory)
	if e != nil {
		return nil, e
	} // Root confines filesystem traversal and symlink resolution to the configured artifact.
	index, e := root.ReadFile("index.html")
	if e != nil {
		root.Close()
		return nil, e
	}
	// The filesystem handle lives for the process lifetime; no per-request descriptor is opened here.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "method not allowed", 405)
			return
		}
		if knownConsoleURL(r.URL) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != "HEAD" {
				w.Write(index)
			}
			return
		}
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if !fs.ValidPath(clean) || path.Clean(clean) != clean || strings.Contains(clean, "\\") || strings.HasPrefix(clean, ".") || strings.Contains(clean, "/.") {
			http.NotFound(w, r)
			return
		}
		// Only generated static asset trees and explicit public icons are served. Unknown
		// auth/API paths can never fall back to index.html or a coincidentally named file.
		allowed := strings.HasPrefix(clean, "static/") || strings.HasPrefix(clean, "assets/") || clean == "favicon.ico" || clean == "favicon.svg" || clean == "robots.txt" || clean == "manifest.webmanifest"
		if !allowed || strings.HasSuffix(clean, ".map") {
			http.NotFound(w, r)
			return
		}
		file, e := root.Open(clean)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		info, e := file.Stat()
		if e != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		if clean == "manifest.webmanifest" {
			w.Header().Set("Content-Type", "application/manifest+json")
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		http.ServeContent(w, r, clean, info.ModTime(), file)
	}), nil
}
