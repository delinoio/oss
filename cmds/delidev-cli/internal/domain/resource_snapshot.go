// SPDX-License-Identifier: Apache-2.0
package domain

const (
	// MaxResourceDocumentBytes is the ordinary resource document bound shared
	// with the TypeScript client decoder.
	MaxResourceDocumentBytes = 1 << 20
	// MaxLargeJobDocumentBytes bounds schema-1 compact-session and recovery
	// workspace-storage Job documents, the only documents above the ordinary bound.
	MaxLargeJobDocumentBytes = 4 << 20
	// MaxLargeJobSnapshotBytes is the complete GetSnapshot response budget for a
	// scope that contains such a Job. Protobuf-JSON expands document bytes to
	// Base64, so a Job document near 4 MiB needs more than the ordinary 4 MiB
	// aggregate budget. It is also the transport send/read limit for that RPC.
	MaxLargeJobSnapshotBytes = 8 << 20
)
