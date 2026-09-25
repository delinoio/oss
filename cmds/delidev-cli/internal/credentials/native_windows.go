//go:build windows

package credentials

import (
	"context"
	"errors"

	"github.com/danieljoos/wincred"
	"golang.org/x/sys/windows"
)

type windowsStore struct{}

func newNative() (nativeStore, error) { return windowsStore{}, nil }
func windowsError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, wincred.ErrElementNotFound):
		return missing()
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return locked()
	case errors.Is(err, windows.ERROR_NO_SUCH_LOGON_SESSION):
		return unavailable()
	default:
		return unavailable()
	}
}
func (windowsStore) get(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := wincred.GetGenericCredential(nativeService + "/" + name)
	if err != nil {
		return nil, windowsError(err)
	}
	if len(c.CredentialBlob) != nativeMaterialSize {
		clear(c.CredentialBlob)
		return nil, recovery()
	}
	return c.CredentialBlob, nil
}
func (s windowsStore) create(ctx context.Context, name string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(value) != nativeMaterialSize {
		return recovery()
	}
	// CredWrite replaces records. The exclusive Vault lock plus this exact lookup
	// prevents replacement of an existing immutable reference on normal retries.
	old, err := wincred.GetGenericCredential(nativeService + "/" + name)
	if err == nil {
		clear(old.CredentialBlob)
		return duplicate()
	}
	if !errors.Is(err, wincred.ErrElementNotFound) {
		return windowsError(err)
	}
	c := wincred.NewGenericCredential(nativeService + "/" + name)
	c.CredentialBlob = value
	c.Persist = wincred.PersistLocalMachine
	return windowsError(c.Write())
}
func (windowsStore) remove(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c := wincred.NewGenericCredential(nativeService + "/" + name)
	err := c.Delete()
	if errors.Is(err, wincred.ErrElementNotFound) {
		return nil
	}
	return windowsError(err)
}
