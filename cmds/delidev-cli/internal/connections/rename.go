package connections

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const maxProfileRecordBytes = 1 << 20
const maxProfileRenames = 1024

// Name edits never replace the original pairing name/grant. One atomic private
// record retains both the latest display name and every exact mutation receipt.
type renameReceipt struct {
	RequestID        domain.ID `json:"request_id"`
	ExpectedRevision uint64    `json:"expected_revision"`
	Name             string    `json:"name"`
}

func (r record) displayName() string {
	if len(r.Renames) != 0 {
		return r.Renames[len(r.Renames)-1].Name
	}
	return r.Name
}

func (r record) validateRenames() error {
	if len(r.Renames) > maxProfileRenames {
		return invalid()
	}
	seen := make(map[domain.ID]bool, len(r.Renames))
	for i, change := range r.Renames {
		if change.RequestID.Validate() != nil || seen[change.RequestID] || change.ExpectedRevision != uint64(i)+1 || validateName(change.Name) != nil {
			return invalid()
		}
		seen[change.RequestID] = true
	}
	return nil
}

// Rename is offline client infrastructure. It never contacts the server,
// rotates credentials, completes pairing, or changes the original authority.
func Rename(ctx context.Context, root string, id, request domain.ID, revision uint64, name string) (Metadata, error) {
	if err := request.Validate(); err != nil {
		return Metadata{}, err
	}
	if err := validateName(name); err != nil {
		return Metadata{}, err
	}
	if revision == 0 {
		return Metadata{}, domain.Fail(domain.InvalidArgument, "An original connection revision is required.", "Inspect the saved connection before renaming it.")
	}
	if _, err := load(root, id); err != nil {
		return Metadata{}, err
	}
	lock, err := security.TryLockExisting(filepath.Join(root, "connections.lock"))
	if err != nil {
		return Metadata{}, err
	}
	defer lock.Close()
	if err := ctx.Err(); err != nil {
		return Metadata{}, domain.SafeError(err)
	}
	value, err := load(root, id)
	if err != nil {
		return Metadata{}, err
	}
	current, err := inspect(root, value)
	if err != nil {
		return Metadata{}, err
	}
	// Bind request identity across the bounded saved-profile inventory. An old
	// retry returns current metadata without reapplying a superseded name.
	profiles, err := List(root)
	if err != nil {
		return Metadata{}, err
	}
	for _, profile := range profiles {
		other, err := load(root, profile.ID)
		if err != nil {
			return Metadata{}, err
		}
		for _, receipt := range other.Renames {
			if receipt.RequestID != request {
				continue
			}
			if profile.ID != id || receipt.ExpectedRevision != revision || receipt.Name != name {
				return Metadata{}, domain.Fail(domain.Conflict, "This connection request already belongs to another name edit.", "Retry its exact original connection, revision and name.")
			}
			return current, nil
		}
	}
	if current.Revision != revision {
		return Metadata{}, domain.Fail(domain.Conflict, "The saved connection changed since this edit began.", "Refresh its name and revision; the staged edit was not applied.")
	}
	if len(value.Renames) >= maxProfileRenames {
		return Metadata{}, domain.Fail(domain.ResourceExhausted, "This saved connection reached its retained name-edit limit.", "Preserve its mutation history; no receipt was discarded or name changed.")
	}
	value.Renames = append(value.Renames, renameReceipt{RequestID: request, ExpectedRevision: revision, Name: name})
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxProfileRecordBytes {
		return Metadata{}, invalid()
	}
	defer clear(raw)
	if err := ctx.Err(); err != nil {
		return Metadata{}, domain.SafeError(err)
	}
	if err := security.WriteAtomic(filepath.Join(profileRoot(root, id), "connection.json"), raw); err != nil {
		return Metadata{}, err
	}
	return inspect(root, value)
}
