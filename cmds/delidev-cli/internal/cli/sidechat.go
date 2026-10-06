// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func requireSidechatSupport(ctx context.Context, c client) error {
	status, err := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
	if err != nil {
		return rpc.ClientError(err)
	}
	if !slices.Contains(status.Msg.Capabilities, pb.SystemCapability_SYSTEM_CAPABILITY_NATIVE_SIDECHAT_V1) {
		return domain.SidechatUnavailable()
	}
	return nil
}

func sidechatCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	if len(args) == 0 || args[0] != "send" {
		return sessionForkPurposeCommand(ctx, c, o, args, pb.ForkPurpose_FORK_PURPOSE_SIDECHAT)
	}
	f := flags("session sidechat send")
	id := f.String("id", "", "original Sidechat session")
	revision := f.Uint64("revision", 0, "exact Sidechat revision")
	parent := f.String("parent-id", "", "original parent session")
	parentRevision := f.Uint64("parent-revision", 0, "exact parent revision")
	messages := f.String("messages", "", "selected complete assistant messages as ID:REV,ID:REV")
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if err := requireSidechatSupport(ctx, c); err != nil {
		return nil, err
	}
	selected := []*pb.SidechatFindingSelection{}
	for _, value := range strings.Split(*messages, ",") {
		parts := strings.Split(value, ":")
		if len(parts) != 2 || domain.ID(parts[0]).Validate() != nil {
			return nil, domain.SidechatUnavailable()
		}
		rev, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil || rev == 0 || strconv.FormatUint(rev, 10) != parts[1] {
			return nil, domain.SidechatUnavailable()
		}
		selected = append(selected, &pb.SidechatFindingSelection{MessageId: parts[0], ExpectedRevision: rev})
	}
	response, err := c.sessions.SendSidechatFindings(ctx, request(c, &pb.SendSidechatFindingsRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}, ParentId: *parent, ExpectedParentRevision: *parentRevision, Messages: selected}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	return sessionChangeJSON(response.Msg.Change), nil
}
