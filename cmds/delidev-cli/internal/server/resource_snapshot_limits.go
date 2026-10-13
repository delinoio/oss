// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// typedLargeJobDocument applies the same closed rule as the TypeScript
// decodeResourceDocument helper: only compact-session Jobs and workspace-storage
// Jobs whose input action is recover may exceed the ordinary document bound.
// Full contextual validation stays with the owners that admit and use the Job.
func typedLargeJobDocument(document []byte) bool {
	var job struct {
		Type  domain.JobType  `json:"type"`
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(document, &job) != nil || !bytes.HasPrefix(bytes.TrimLeft(job.Input, " \t\r\n"), []byte("{")) {
		return false
	}
	switch job.Type {
	case domain.CompactSessionJob:
		return true
	case domain.WorkspaceStorageJob:
		var input struct {
			Action workspace.StorageAction `json:"action"`
		}
		return json.Unmarshal(job.Input, &input) == nil && input.Action == workspace.StorageRecover
	}
	return false
}

func largeJobCandidate(resource *pb.Resource) bool {
	return resource.Kind == pb.EntityKind_ENTITY_KIND_JOB && resource.SchemaVersion == 1
}

// snapshotContainsLargeJob reports whether a scope holds a typed Job that needs
// the larger snapshot budget: one above the ordinary document bound but within
// the typed-Job bound. Smaller typed Jobs fit the ordinary 4 MiB budget.
func snapshotContainsLargeJob(resources []*pb.Resource) bool {
	for _, resource := range resources {
		size := len(resource.DocumentJson)
		if largeJobCandidate(resource) && size > domain.MaxResourceDocumentBytes && size <= domain.MaxLargeJobDocumentBytes && typedLargeJobDocument(resource.DocumentJson) {
			return true
		}
	}
	return false
}

// snapshotUnreadableDocument returns a resource whose document exceeds the
// bound that the client decoder accepts for it. Scopes using the enlarged
// budget must not carry documents that the client would reject anyway.
func snapshotUnreadableDocument(resources []*pb.Resource) *pb.Resource {
	for _, resource := range resources {
		size := len(resource.DocumentJson)
		if size <= domain.MaxResourceDocumentBytes {
			continue
		}
		if !largeJobCandidate(resource) || size > domain.MaxLargeJobDocumentBytes || !typedLargeJobDocument(resource.DocumentJson) {
			return resource
		}
	}
	return nil
}

func snapshotTooLarge() error {
	return domain.Fail(domain.ResourceExhausted, "The snapshot scope exceeds its response byte limit.", "Use a narrower session/project scope; do not treat partial state as a coherent snapshot.")
}

func (s *Service) logSnapshotRejected(reason string, f store.Filter, resourceCount, limit int, resource *pb.Resource) {
	if s.logger == nil {
		return
	}
	attributes := []any{"reason", reason, "kind", f.Kind, "session_id", f.SessionID, "project_id", f.ProjectID, "resource_count", resourceCount, "limit_bytes", limit}
	if resource != nil {
		attributes = append(attributes, "resource_id", resource.Id, "document_bytes", len(resource.DocumentJson))
	}
	s.logger.Warn("snapshot_rejected", attributes...)
}
