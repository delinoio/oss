package userservice

import (
	"net/url"
	"path/filepath"
	"testing"
)

func TestUserBusAddressHasNoAutolaunchOrRemoteFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session bus")
	escaped := url.PathEscape(path)
	for _, address := range []string{"unix:path=" + escaped, "unix:guid=0123456789abcdef0123456789abcdef,path=" + escaped} {
		if got, err := existingBusPath(address, ""); err != nil || got != path {
			t.Fatal("existing single user-bus path rejected", err)
		}
	}
	for _, address := range []string{
		"autolaunch:", "tcp:host=localhost,port=1", "unix:abstract=session", "unix:path=relative",
		"unix:path=" + path + ";unix:path=" + path,
		"unix:path=" + path + ",path=" + path, "unix:path=" + path + ",guid=invalid",
		"unix:path=" + path + "%00", "unix:path=" + path + "%zz", "unix:path=" + path + ",noncefile=foreign",
	} {
		if _, err := existingBusPath(address, t.TempDir()); err == nil {
			t.Fatal("unsafe user-bus address accepted")
		}
	}
	if _, err := existingBusPath("", ""); err == nil {
		t.Fatal("missing runtime autolaunched a bus")
	}
}
