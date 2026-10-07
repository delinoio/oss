package credentials

import (
	"context"
	"runtime"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// nativeStore stores profile-bounded native material under an exact opaque
// DeliDev reference. Implementations never enumerate unrelated credentials, launch
// a password-bearing command or fall back to disk. On macOS,
// the OS may display authentication UI in the server user's session.
// The enclosing Vault or PATStore holds an exclusive scope lock through every operation.
type nativeStore interface {
	get(context.Context, string) ([]byte, error)
	create(context.Context, string, []byte) error
	remove(context.Context, string) error
}

const nativeMaterialSize = 64
const nativeService = "io.delino.delidev.credentials.v1"

func unavailable() error {
	return domain.Fail(domain.Unavailable, "The OS credential store is unavailable.", "Start the server in a user session with an available OS credential store, then retry the same request.")
}
func locked() error {
	if runtime.GOOS == "darwin" {
		return domain.Fail(domain.ConfirmationRequired, "The OS credential store requires user authentication.", "Approve the macOS Keychain authentication request in the server user's session. If it was canceled, denied or could not be displayed, retry the same request from a session that can authorize Keychain access. DeliDev does not collect your password or use plaintext storage.")
	}
	return domain.Fail(domain.ConfirmationRequired, "The OS credential store requires user authentication.", "Unlock or authorize the OS credential store in the server user's session, then retry the same request. DeliDev does not prompt or use plaintext storage.")
}
func missing() error {
	return domain.Fail(domain.NotFound, "The protected credential reference does not exist.", "Reconnect the account explicitly if its protected credential was removed.")
}
func duplicate() error {
	return domain.Fail(domain.Conflict, "The protected credential reference already exists.", "Retry the same request to reconcile its existing value.")
}
func recovery() error {
	return domain.Fail(domain.RecoveryRequired, "The protected credential cannot be recovered from its recorded state.", "Keep the secret scope intact and reconnect with a new request after resolving or deleting the old reference.")
}
func isCode(err error, code domain.Code) bool {
	return err != nil && domain.SafeError(err).Code == code
}

// The zero profile preserves existing envelope records exactly. PAT bytes have
// their own direct native service and cannot be read as account wrapping keys.
type nativeProfile uint8

const (
	wrappingProfile nativeProfile = iota
	patProfile
)
const MaxPATBytes = 512

func (p nativeProfile) service() string {
	if p == patProfile {
		return "io.delino.delidev.github.pat.v1"
	}
	return nativeService
}
func (p nativeProfile) accepts(size int) bool {
	switch p {
	case wrappingProfile:
		return size == nativeMaterialSize
	case patProfile:
		return size > 0 && size <= MaxPATBytes
	default:
		return false
	}
}
func newNative() (nativeStore, error) { return newNativeProfile(wrappingProfile) }
