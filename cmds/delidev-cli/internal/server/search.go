package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var searchOutcomes = map[pb.SearchExecutionOutcome]domain.ExecutionOutcome{
	pb.SearchExecutionOutcome_SEARCH_EXECUTION_OUTCOME_UNSPECIFIED: "",
	pb.SearchExecutionOutcome_SEARCH_EXECUTION_OUTCOME_NOT_STARTED: domain.ExecutionNotStarted,
	pb.SearchExecutionOutcome_SEARCH_EXECUTION_OUTCOME_RUNNING:     domain.ExecutionRunning,
	pb.SearchExecutionOutcome_SEARCH_EXECUTION_OUTCOME_SUCCEEDED:   domain.ExecutionSucceeded,
	pb.SearchExecutionOutcome_SEARCH_EXECUTION_OUTCOME_FAILED:      domain.ExecutionFailed,
	pb.SearchExecutionOutcome_SEARCH_EXECUTION_OUTCOME_STOPPED:     domain.ExecutionStopped,
}
var searchArchives = map[pb.SearchArchiveState]domain.ArchiveState{
	pb.SearchArchiveState_SEARCH_ARCHIVE_STATE_UNSPECIFIED: "",
	pb.SearchArchiveState_SEARCH_ARCHIVE_STATE_ACTIVE:      domain.NotArchived,
	pb.SearchArchiveState_SEARCH_ARCHIVE_STATE_ARCHIVING:   domain.ArchivePending,
	pb.SearchArchiveState_SEARCH_ARCHIVE_STATE_ARCHIVED:    domain.Archived,
}

func (s *Service) SearchConversations(ctx context.Context, req *connect.Request[pb.SearchConversationsRequest]) (*connect.Response[pb.SearchConversationsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Server authentication is required.", "Use the server token or a registered device credential."), correlation)
	}
	outcome, outcomeOK := searchOutcomes[req.Msg.Outcome]
	archive, archiveOK := searchArchives[req.Msg.Archive]
	if !outcomeOK || !archiveOK {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Unknown search filter.", "Select supported execution and archive states."), correlation)
	}
	f := store.SearchFilter{SearchSelection: domain.SearchSelection{SessionID: domain.ID(req.Msg.SessionId), ProjectID: domain.ID(req.Msg.ProjectId), AgentID: domain.ID(req.Msg.AgentId), AccountID: domain.ID(req.Msg.AccountId), Outcome: outcome, Archive: archive}, Query: req.Msg.Query, Limit: int(req.Msg.PageSize)}
	if f.Limit == 0 {
		f.Limit = 50
	}
	if err := f.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	// Cursors are authenticated, not encrypted. A keyed scope commitment keeps
	// potentially sensitive search text out of the client-visible token and
	// prevents dictionary recovery using a public unsalted query digest.
	raw, _ := json.Marshal(struct {
		Query     string
		Selection domain.SearchSelection
		Actor     domain.Principal
	}{strings.ToLower(f.Query), f.SearchSelection, actor})
	mac := hmac.New(sha256.New, []byte(s.Identity.Token))
	mac.Write([]byte("conversation-search-v1\x00"))
	mac.Write(raw)
	scope := "search:" + hex.EncodeToString(mac.Sum(nil))
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		if cursor.After == "" {
			return nil, rpc.Error(domain.Fail(domain.CursorExpired, "The search page position is missing.", "Restart search pagination."), correlation)
		}
		f.After, f.Epoch = cursor.After, cursor.Sequence
	}
	// Short queries may require a scan. A slow search must release SQLite's
	// single connection promptly so independent jobs can continue.
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result := &pb.SearchConversationsResponse{}
	err := s.Store.Read(bounded, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		rows, more, epoch, err := tx.SearchMessages(f)
		if err != nil {
			return err
		}
		used := 0
		for _, record := range rows {
			sr, err := tx.Get(domain.SessionKind, record.SessionID)
			if err != nil {
				return err
			}
			session, err := store.Decode[domain.Session](sr)
			if err != nil {
				return err
			}
			hit := &pb.ConversationSearchHit{Message: rpc.Resource(record), SessionName: session.Name, AgentId: string(session.AgentID)}
			for wire, value := range searchOutcomes {
				if value == session.Outcome {
					hit.Outcome = wire
				}
			}
			for wire, value := range searchArchives {
				if value == session.Archive {
					hit.Archive = wire
				}
			}
			encoded, err := protojson.Marshal(hit)
			if err != nil {
				return err
			}
			size := max(proto.Size(hit), len(encoded)) + 16
			if used+size > maxResourcePageBytes {
				if len(result.Hits) == 0 {
					return domain.Fail(domain.ResourceExhausted, "The search result exceeds its response bound.", "Read the original message by identity.")
				}
				more = true
				break
			}
			result.Hits = append(result.Hits, hit)
			used += size
		}
		if more && len(result.Hits) > 0 {
			result.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, After: domain.ID(result.Hits[len(result.Hits)-1].Message.Id), Sequence: epoch})
		}
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.DebugContext(ctx, "conversation_search_completed", "correlation_id", correlation, "result_count", len(result.Hits), "has_more", result.NextPageToken != "")
	response := connect.NewResponse(result)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
