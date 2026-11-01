//go:build linux || darwin

package session

import (
	"net"
	"os"
	"path/filepath"
	"time"
)

func listen() (net.Listener, string, func(), error) {
	d, e := os.MkdirTemp("/tmp", "ccow-s-")
	if e != nil {
		return nil, "", func() {}, e
	}
	p := filepath.Join(d, "ipc")
	l, e := net.Listen("unix", p)
	return l, p, func() { os.RemoveAll(d) }, e
}
func dial(p string) (net.Conn, error) { return net.DialTimeout("unix", p, 10*time.Second) }
