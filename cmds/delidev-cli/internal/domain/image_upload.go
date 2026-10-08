// SPDX-License-Identifier: Apache-2.0
package domain

type ImageUploadState string

const (
	ImageUploading ImageUploadState = "uploading"
	ImageReady     ImageUploadState = "ready"
	ImageClaimed   ImageUploadState = "claimed"
	ImageDeleting  ImageUploadState = "deleting"
	ImageDeleted   ImageUploadState = "deleted"
)
const ImageAttachmentJob JobType = "image-attachment"

// ImageUpload is durable metadata only. Bytes remain on its original Worker.
type ImageUpload struct {
	Actor           Principal        `json:"actor"`
	Version         uint32           `json:"version"`
	Attachment      ImageAttachment  `json:"attachment"`
	DraftID         ID               `json:"draft_id"`
	OperationID     ID               `json:"operation_id"`
	MachineRevision uint64           `json:"machine_revision,string"`
	SessionID       ID               `json:"session_id,omitempty"`
	InputID         ID               `json:"input_id,omitempty"`
	State           ImageUploadState `json:"state"`
	UploadedBytes   uint64           `json:"uploaded_bytes"`
	Owners          []ID             `json:"owners,omitempty"`
}
