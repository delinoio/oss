// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const maxNativeReviewPublications = 132 // Ready, entered/exited and 128 original responses.
const nativeReviewPublicationPrefix = "review-observation-"

type nativeReviewPublication struct {
	Version          uint32    `json:"version"`
	AssignmentDigest string    `json:"assignment_digest"`
	ServerID         domain.ID `json:"server_id"`
	DeviceID         domain.ID `json:"device_id"`
	Request          []byte    `json:"request"`
}

// Retain exact observation bytes and receipt identity before publication. This
// journal can replay only server evidence; it has no original native sender.
func publishNativeReviewObservation(ctx context.Context, c *PublicationConfig, root string, sequence uint64, request *pb.PublishNativeCodeReviewRequest) error {
	if sequence == 0 || sequence > maxNativeReviewPublications || c == nil || c.Assignment == nil {
		return publicationUncertain()
	}
	raw, err := proto.Marshal(request)
	if err != nil {
		return publicationUncertain()
	}
	path := filepath.Join(root, fmt.Sprintf("%s%06d.json", nativeReviewPublicationPrefix, sequence))
	entry := nativeReviewPublication{Version: 1, AssignmentDigest: executionInputDigest(c.Assignment.DocumentJson), ServerID: c.Credential.ServerID, DeviceID: c.Credential.DeviceID, Request: raw}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return publicationUncertain()
	}
	if err := writeJSON(path, entry); err != nil {
		return err
	}
	return sendNativeReviewObservation(ctx, c, path, entry)
}

func sendNativeReviewObservation(ctx context.Context, c *PublicationConfig, path string, entry nativeReviewPublication) error {
	request := &pb.PublishNativeCodeReviewRequest{}
	if c == nil || c.Assignment == nil || c.Client == nil || entry.Version != 1 || entry.ServerID != c.Credential.ServerID || entry.DeviceID != c.Credential.DeviceID || entry.AssignmentDigest != executionInputDigest(c.Assignment.DocumentJson) || len(entry.Request) > domain.MaxPromptBytes+16384 || proto.Unmarshal(entry.Request, request) != nil || request.Mutation == nil || domain.ID(request.Mutation.RequestId).Validate() != nil || request.Mutation.Id != c.Assignment.Id || request.Mutation.ExpectedRevision != c.Assignment.Revision || request.MachineId != string(c.Credential.MachineID) || request.InstanceId != string(c.Instance) {
		return publicationUncertain()
	}
	digest := sha256.Sum256(entry.Request)
	ack := []byte(hex.EncodeToString(digest[:]))
	if retained, err := security.ReadPrivate(path+".ack", 64); err == nil {
		if !bytes.Equal(retained, ack) {
			return publicationUncertain()
		}
		return nil
	} else if !os.IsNotExist(err) {
		return publicationUncertain()
	}
	// Cancellation fences inference, but cannot erase an already-observed
	// lifecycle or usage receipt. Keep the original bounded evidence owner.
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	var returned error
	for attempt := 0; attempt < 3; attempt++ {
		call, stop := context.WithTimeout(bounded, 10*time.Second)
		_, err := c.Client.PublishNativeCodeReview(call, authenticated(c.Credential, request))
		stop()
		if err == nil {
			return security.WriteAtomic(path+".ack", ack)
		}
		returned = rpc.ClientError(err)
		if bounded.Err() != nil || connect.CodeOf(err) != connect.CodeUnavailable && connect.CodeOf(err) != connect.CodeDeadlineExceeded {
			return returned
		}
	}
	return returned
}

// Reconnect settles retained exact publication requests before the terminal job
// report. Never rerun native review/start to recover an unacknowledged receipt.
func replayNativeReviewObservations(ctx context.Context, config Config, c *PublicationConfig) error {
	if c == nil || c.Assignment == nil || domain.ID(c.Assignment.Id).Validate() != nil {
		return publicationUncertain()
	}
	root := filepath.Join(config.Root, "jobs", c.Assignment.Id)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return publicationUncertain()
	}
	var names []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), nativeReviewPublicationPrefix) && strings.HasSuffix(entry.Name(), ".json") {
			if entry.IsDir() || len(names) >= maxNativeReviewPublications {
				return publicationUncertain()
			}
			names = append(names, entry.Name())
		}
	}
	slices.Sort(names)
	for index, name := range names {
		if name != fmt.Sprintf("%s%06d.json", nativeReviewPublicationPrefix, index+1) {
			return publicationUncertain()
		}
		path := filepath.Join(root, name)
		raw, err := security.ReadPrivate(path, domain.MaxPromptBytes*2+32768)
		var entry nativeReviewPublication
		if err != nil || domain.Decode(raw, &entry) != nil {
			return publicationUncertain()
		}
		if err := sendNativeReviewObservation(ctx, c, path, entry); err != nil {
			return err
		}
	}
	return nil
}
