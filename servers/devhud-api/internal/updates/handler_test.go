package updates

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestServesOnlyExactStableTargetsAndInstalledPackage(t *testing.T) {
	manifest := []byte(`{"schemaVersion":1}`)
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
	handler := NewHandler(fstest.MapFS{"stable/windows/aarch64/windows-msi.json": {Data: []byte(`{"schemaVersion":1}`)}})
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
