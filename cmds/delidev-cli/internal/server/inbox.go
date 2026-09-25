package server

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
)

const maxInboxViewBytes = 3 << 20

func inboxActor(ctx context.Context) (domain.Principal, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return domain.Principal{}, domain.Fail(domain.PermissionDenied, "Only an owner or paired client can access the inbox.", "Use an authorized product client; inbox state never grants Worker execution authority.")
	}
	return actor, nil
}

func inboxReadState(value pb.InboxReadState, allowAll bool) (domain.InboxReadState, error) {
	switch value {
	case pb.InboxReadState_INBOX_READ_STATE_UNSPECIFIED:
		if allowAll {
			return "", nil
		}
	case pb.InboxReadState_INBOX_READ_STATE_READ:
		return domain.InboxRead, nil
	case pb.InboxReadState_INBOX_READ_STATE_UNREAD:
		return domain.InboxUnread, nil
	}
	return "", domain.Fail(domain.InvalidArgument, "An explicit valid inbox read state is required.", "Select read or unread; this never resolves the source request.")
}

func inboxSource(value pb.InboxSource) (domain.InboxSource, error) {
	switch value {
	case pb.InboxSource_INBOX_SOURCE_UNSPECIFIED:
		return "", nil
	case pb.InboxSource_INBOX_SOURCE_INTERACTION:
		return domain.InteractionInbox, nil
	case pb.InboxSource_INBOX_SOURCE_EXECUTION_TERMINAL:
		return domain.ExecutionTerminalInbox, nil
	}
	return "", domain.Fail(domain.InvalidArgument, "Unknown inbox source filter.", "Select interaction or execution-terminal, or omit the filter.")
}

func currentInboxView(tx *store.Tx, record store.Record) (*pb.InboxView, error) {
	entry, err := store.Decode[domain.InboxEntry](record)
	if err != nil {
		return nil, err
	}
	if record.Kind != domain.InboxKind || entry.Validate() != nil {
		return nil, domain.Fail(domain.RecoveryRequired, "The retained inbox source is inconsistent.", "Preserve the original inbox and source records for reconciliation.")
	}
	session, err := tx.Get(domain.SessionKind, record.SessionID)
	if err != nil {
		return nil, err
	}
	view := &pb.InboxView{Entry: rpc.Resource(record), Session: rpc.Resource(session)}
	if entry.Source == domain.InteractionInbox {
		original, err := tx.Get(domain.InteractionKind, entry.SourceID)
		if err != nil {
			return nil, err
		}
		if original.SessionID != record.SessionID || original.ProjectID != record.ProjectID {
			return nil, domain.Fail(domain.RecoveryRequired, "The inbox source belongs to another retained scope.", "Reconcile the original request before responding; do not substitute another source.")
		}
		view.Interaction = rpc.Resource(original)
	}
	if proto.Size(view) > maxInboxViewBytes {
		return nil, domain.Fail(domain.ResourceExhausted, "The joined inbox view exceeds its bound.", "Inspect the retained source resources separately without truncating their content.")
	}
	return view, nil
}

func (s *Service) GetInboxEntry(ctx context.Context, req *connect.Request[pb.GetInboxEntryRequest]) (*connect.Response[pb.GetInboxEntryResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := inboxActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	view, err := s.readInboxView(ctx, domain.ID(req.Msg.Id))
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.GetInboxEntryResponse{View: view})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) readInboxView(ctx context.Context, id domain.ID) (*pb.InboxView, error) {
	var view *pb.InboxView
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		r, err := tx.Get(domain.InboxKind, id)
		if err != nil {
			return err
		}
		view, err = currentInboxView(tx, r)
		return err
	})
	return view, err
}

func (s *Service) ListInbox(ctx context.Context, req *connect.Request[pb.ListInboxRequest]) (*connect.Response[pb.ListInboxResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := inboxActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	state, err := inboxReadState(req.Msg.ReadState, true)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	source, err := inboxSource(req.Msg.Source)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	limit := req.Msg.PageSize
	if limit == 0 {
		limit = 50
	}
	f := store.InboxFilter{SessionID: domain.ID(req.Msg.SessionId), ProjectID: domain.ID(req.Msg.ProjectId), Source: source, ReadState: state, Limit: int(limit)}
	if err := f.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	scope := fmt.Sprintf("inbox:%s:%s:%s:%s", f.SessionID, f.ProjectID, f.Source, f.ReadState)
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		if cursor.After == "" {
			return nil, rpc.Error(domain.Fail(domain.CursorExpired, "The inbox page position is missing.", "Restart inbox pagination."), correlation)
		}
		f.After, f.Epoch = cursor.After, cursor.Sequence
	}
	result := &pb.ListInboxResponse{}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		rows, more, epoch, err := tx.InboxPage(f)
		if err != nil {
			return err
		}
		viewBytes := 0
		for _, record := range rows {
			view, err := currentInboxView(tx, record)
			if err != nil {
				return err
			}
			size := proto.Size(view) + 8 // Include each repeated message's wire framing.
			if viewBytes+size > maxInboxViewBytes {
				if len(result.Entries) == 0 {
					return domain.Fail(domain.ResourceExhausted, "The inbox entry exceeds its page bound.", "Inspect the original entry and source resources separately.")
				}
				more = true
				break
			}
			result.Entries = append(result.Entries, view)
			viewBytes += size
		}
		if more && len(result.Entries) > 0 {
			last := result.Entries[len(result.Entries)-1].Entry.Id
			result.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, After: domain.ID(last), Sequence: epoch})
		}
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(result)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

type inboxReadIdentity struct {
	ID       domain.ID
	Revision uint64
	State    domain.InboxReadState
	Actor    domain.Principal
}

type inboxReadReceipt struct {
	ID domain.ID `json:"inbox_id"`
}

func (s *Service) SetInboxReadState(ctx context.Context, req *connect.Request[pb.SetInboxReadStateRequest]) (*connect.Response[pb.SetInboxReadStateResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := inboxActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := validateSessionMutation(req.Msg.Mutation); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	state, err := inboxReadState(req.Msg.ReadState, false)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	identity := inboxReadIdentity{domain.ID(meta.Id), meta.ExpectedRevision, state, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "inbox.read-state", identity, func(tx *store.Tx) (any, error) {
		r, err := tx.SetInboxReadState(identity.ID, identity.Revision, identity.State)
		if err != nil {
			return nil, err
		}
		// Validate the joined source before committing the mark. A broken
		// retained reference must not produce a partial accepted mutation.
		if _, err := currentInboxView(tx, r); err != nil {
			return nil, err
		}
		return inboxReadReceipt{r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt inboxReadReceipt
	if domain.Decode(result.Data, &receipt) != nil || receipt.ID != identity.ID {
		return nil, rpc.Error(domain.Fail(domain.NotFound, "The original inbox entry is no longer retained.", "Refresh the inbox; an old receipt cannot recreate a deleted source."), correlation)
	}
	view, err := s.readInboxView(ctx, receipt.ID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "inbox_read_state_recorded", "correlation_id", correlation, "inbox_id", receipt.ID, "request_id", result.RequestID, "requested_state", state, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.SetInboxReadStateResponse{View: view, RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
