package harness

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// ResolveExecution selects one executable without launching a version child or
// a protocol probe. Explicit selection failures never fall back to PATH.
func ResolveExecution(ctx context.Context, selection domain.ExecutionStartupSelection) (domain.Installation, error) {
	path, state := resolve(selection.Harness, selection.ExplicitPath)
	code := domain.Unsupported
	switch state {
	case domain.InstallationMissing:
		code = domain.NotFound
	case domain.InstallationDenied:
		code = domain.PermissionDenied
	case domain.InstallationFailed:
		code = domain.Unavailable
	case domain.InstallationUnchecked:
		digest, err := InspectExecutable(ctx, path)
		if err != nil {
			return domain.Installation{}, err
		}
		if selection.ExecutableSHA256 != "" && selection.ExecutableSHA256 != digest {
			return domain.Installation{}, domain.Fail(domain.RecoveryRequired, "The original native executable changed.", "Restore the original executable before continuing its native history.")
		}
		return domain.Installation{Harness: selection.Harness, ExplicitPath: selection.ExplicitPath, ResolvedPath: path, ExecutableSHA256: digest}, nil
	}
	return domain.Installation{}, domain.Fail(code, "The selected agent executable cannot be started.", "Install the harness or correct its path and permissions on the selected Runner Device.")
}
