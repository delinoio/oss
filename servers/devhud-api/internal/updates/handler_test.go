package updates

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

const validManifest = `{"schemaVersion":1,"signedPayload":"e30=","manifestSignature":"c2lnbmF0dXJl"}`

type countingFS struct {
	inner     fs.FS
	bytesRead int
}

func (filesystem *countingFS) Open(name string) (fs.File, error) {
	file, err := filesystem.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &countingFile{File: file, bytesRead: &filesystem.bytesRead}, nil
}

type countingFile struct {
	fs.File
	bytesRead *int
}

func (file *countingFile) Read(buffer []byte) (int, error) {
	read, err := file.File.Read(buffer)
	*file.bytesRead += read
	return read, err
}

func TestServesOnlyExactStableTargetsAndInstalledPackage(t *testing.T) {
	manifest := []byte(validManifest)
	handler := NewHandler(fstest.MapFS{"stable/linux/x86_64/linux-appimage.json": {Data: manifest}})
	request := httptest.NewRequest(http.MethodGet, "/updates/stable/linux/x86_64.json", nil)
	request.SetPathValue("channel", "stable")
	request.SetPathValue("platform", "linux")
	request.SetPathValue("artifact", "x86_64.json")
	request.Header.Set("X-DevHud-Package", "linux-appimage")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != string(manifest) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	for header, expected := range map[string]string{
		"Cache-Control": "no-store", "Vary": "X-DevHud-Package", "X-Content-Type-Options": "nosniff",
	} {
		if response.Header().Get(header) != expected {
			t.Fatalf("%s = %q", header, response.Header().Get(header))
		}
	}
}

func TestHeadHasMetadataWithoutManifestBody(t *testing.T) {
	handler := NewHandler(fstest.MapFS{"stable/windows/aarch64/windows-msi.json": {Data: []byte(validManifest)}})
	request := httptest.NewRequest(http.MethodHead, "/updates/stable/windows/aarch64.json", nil)
	request.SetPathValue("channel", "stable")
	request.SetPathValue("platform", "windows")
	request.SetPathValue("artifact", "aarch64.json")
	request.Header.Set("X-DevHud-Package", "windows-msi")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.Len() != 0 || response.Header().Get("Content-Length") == "" {
		t.Fatalf("HEAD response = %d body=%q headers=%v", response.Code, response.Body.String(), response.Header())
	}
}

func TestRejectsStructurallyInvalidStoredManifests(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		manifest string
	}{
		{"null", `null`},
		{"empty object", `{}`},
		{"wrong schema", `{"schemaVersion":2,"signedPayload":"e30=","manifestSignature":"c2lnbmF0dXJl"}`},
		{"missing payload", `{"schemaVersion":1,"manifestSignature":"c2lnbmF0dXJl"}`},
		{"empty payload", `{"schemaVersion":1,"signedPayload":"","manifestSignature":"c2lnbmF0dXJl"}`},
		{"missing signature", `{"schemaVersion":1,"signedPayload":"e30="}`},
		{"empty signature", `{"schemaVersion":1,"signedPayload":"e30=","manifestSignature":""}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			handler := NewHandler(fstest.MapFS{
				"stable/linux/x86_64/linux-appimage.json": {Data: []byte(testCase.manifest)},
			})
			request := httptest.NewRequest(http.MethodGet, "/updates/stable/linux/x86_64.json", nil)
			request.SetPathValue("channel", "stable")
			request.SetPathValue("platform", "linux")
			request.SetPathValue("artifact", "x86_64.json")
			request.Header.Set("X-DevHud-Package", "linux-appimage")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
			}
		})
	}
}

func TestRejectsUnsupportedMissingAndMalformedRequests(t *testing.T) {
	handler := NewHandler(fstest.MapFS{"stable/linux/x86_64/linux-deb.json": {Data: []byte(`not-json`)}})
	for _, testCase := range []struct {
		name, platform, architecture, packageKind string
		expected                                  int
	}{
		{"unknown architecture", "linux", "mips64", "linux-deb", http.StatusBadRequest},
		{"wrong package", "linux", "x86_64", "windows-msi", http.StatusBadRequest},
		{"malformed stored manifest", "linux", "x86_64", "linux-deb", http.StatusServiceUnavailable},
		{"missing", "darwin", "x86_64", "macos-app", http.StatusNotFound},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/updates/stable/"+testCase.platform+"/"+testCase.architecture+".json", nil)
			request.SetPathValue("channel", "stable")
			request.SetPathValue("platform", testCase.platform)
			request.SetPathValue("artifact", testCase.architecture+".json")
			request.Header.Set("X-DevHud-Package", testCase.packageKind)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != testCase.expected {
				t.Fatalf("status = %d, want %d", response.Code, testCase.expected)
			}
		})
	}
}

func TestRejectsOversizedManifestAfterBoundedRead(t *testing.T) {
	manifestPath := "stable/linux/x86_64/linux-appimage.json"
	manifests := &countingFS{inner: fstest.MapFS{
		manifestPath: {Data: bytes.Repeat([]byte("x"), maxManifestBytes*2)},
	}}
	handler := NewHandler(manifests)
	request := httptest.NewRequest(http.MethodGet, "/updates/stable/linux/x86_64.json", nil)
	request.SetPathValue("channel", "stable")
	request.SetPathValue("platform", "linux")
	request.SetPathValue("artifact", "x86_64.json")
	request.Header.Set("X-DevHud-Package", "linux-appimage")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if manifests.bytesRead != maxManifestBytes+1 {
		t.Fatalf("read %d manifest bytes, want %d", manifests.bytesRead, maxManifestBytes+1)
	}
}
