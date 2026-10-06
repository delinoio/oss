// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Fork initializes a fresh runtime and copies only the closed SQLite profile.
// Bound its new private file descriptors separately from the source inventory;
// native auxiliary growth outside this profile cannot grant retention authority.
const maxForkRuntimeInventoryBytes = 64 << 10

func forkCheckpointCapacity() error {
	return domain.Fail(domain.ResourceExhausted, "The complete OpenCode fork checkpoint exceeds its private storage capacity.", "Keep the original session; start a new session with selected text instead of repeating Fork.")
}

// This upper envelope includes both copies of cloned histories, the complete
// original histories and identity proof, and conservatively retains every source
// file descriptor even though replacement copies only SQLite. Placeholder bytes
// reserve the declared encoded bounds; they never become native identities.
func checkForkCheckpointCapacity(source nativeCheckpoint) error {
	histories := checkpointHistories(source)
	cloned := make([]HistoryObservation, len(histories))
	identities := []ForkMessageIdentity{}
	sessionID := "ses_000000000000abcdefghijklmn"
	messageID := "msg_000000000000abcdefghijklmn"
	partID := "prt_000000000000abcdefghijklmn"
	digest := strings.Repeat("f", 64)
	for index, history := range histories {
		next := copyHistoryObservation(history)
		next.SessionID, next.InputID, next.AssistantID, next.Digest = sessionID, messageID, messageID, digest
		for m, message := range history.Messages {
			identity := ForkMessageIdentity{Source: message.ID, Child: messageID, Parts: []ForkPartIdentity{}}
			next.Messages[m].ID, next.Messages[m].Digest = messageID, digest
			for p, part := range message.Parts {
				identity.Parts = append(identity.Parts, ForkPartIdentity{Source: part.ID, Child: partID})
				next.Messages[m].Parts[p].ID, next.Messages[m].Parts[p].Digest = partID, digest
			}
			identities = append(identities, identity)
		}
		cloned[index] = next
	}
	if len(cloned) == 0 {
		return incompatible()
	}
	files := slices.Clone(source.Files)
	for index := range files {
		if !files[index].Directory {
			files[index].Size = maxCheckpointFileBytes
		}
	}
	// JSON can encode a path byte as six bytes. Canonical directory validation
	// permits at most 32,768 UTF-8 bytes; native slug permits at most 256 bytes.
	path := strings.Repeat("\x01", 32768)
	ref := source.Reference
	ref.SHA256, ref.SessionID, ref.InputID, ref.PartID, ref.HistorySHA256 = "", sessionID, messageID, partID, digest
	sourceReference := source.Reference
	sourceReference.SHA256 = digest
	proof := &nativeCheckpointFork{Version: 1, RequestID: domain.NewID(), SourceReference: sourceReference, SourceWorkspace: source.Workspace, SourceHistories: histories, ClonedHistories: cloned, Identities: identities, SelectionPending: true}
	value := nativeCheckpoint{Version: 1, NativeVersion: SupportedVersion, Reference: ref, RuntimeHome: path, Workspace: path, NativeRoot: "/", Project: "global", Slug: strings.Repeat("\x01", 256), Created: 9007199254740991, SettingsSHA256: digest, CredentialSHA256: digest, History: cloned[len(cloned)-1], Files: files, Fork: proof}
	if len(cloned) > 1 {
		value.Previous, value.PredecessorSHA256 = cloned[:len(cloned)-1], digest
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxCheckpointBytes-maxForkRuntimeInventoryBytes {
		return forkCheckpointCapacity()
	}
	return nil
}

func validForkRuntimeInventory(files []checkpointFile) bool {
	raw, err := json.Marshal(files)
	return err == nil && len(raw) <= maxForkRuntimeInventoryBytes
}
