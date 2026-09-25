package credentials

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// nativeStore stores only fixed-size wrapping material under an exact opaque
// DeliDev reference. Implementations never enumerate unrelated credentials, launch
// a password-bearing command, display an authentication prompt, or fall back to disk.
// The enclosing Vault holds an exclusive scope lock through every operation.
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
