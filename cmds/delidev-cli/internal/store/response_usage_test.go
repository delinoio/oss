package store

import (
	"context"

	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func responseRecord(f searchFixture) domain.ResponseUsageRecord {
	input, cached, output, reasoning, total := int64(10), int64(2), int64(4), int64(1), int64(14)
	return domain.ResponseUsageRecord{SessionID: f.session, ProjectID: f.project, ExecutionID: f.execution, AccountID: f.account, ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), Harness: domain.Codex, Version: domain.CodexProtocolVersion, ThreadID: string(domain.NewID()), TurnID: string(domain.NewID()), Sequence: 3, Usage: domain.NativeResponseUsage{ResponseDigest: strings.Repeat("a", 64), CostEvidence: domain.UsageCostMissing, Counts: &domain.NativeTokenCounts{Input: &input, Cached: &cached, Output: &output, Reasoning: &reasoning, Total: &total}}}
}

func writeResponse(s *Store, id domain.ID, record domain.ResponseUsageRecord) (domain.ID, bool, error) {
	var retained domain.ID
	var duplicate bool
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.response", record, func(tx *Tx) (any, error) {
		var err error
		retained, duplicate, err = tx.PutResponseUsage(id, record)
		return nil, err
	})
	return retained, duplicate, err
}

func TestResponseUsageDurableIdentityAndDeletion(t *testing.T) {
	ctx := context.Background()
	s, root := openTest(t)
	f := seedSearch(t, s, "source", domain.Archived)
	record := responseRecord(f)
	id := domain.NewID()
	if retained, duplicate, err := writeResponse(s, id, record); err != nil || duplicate || retained != id {
		t.Fatal("initial write", err)
	}
	before, err := s.ResponseUsage(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	record.Sequence++
	if retained, duplicate, err := writeResponse(s, domain.NewID(), record); err != nil || !duplicate || retained != id {
		t.Fatal("restart lost native deduplication", err)
	}
	after, err := s.ResponseUsage(ctx, id)
	if err != nil || after.Record.Sequence != before.Record.Sequence || !after.CreatedAt.Equal(before.CreatedAt) {
		t.Fatal("retry changed original time or sequence", err)
	}
	for _, change := range []string{"execution", "session", "turn", "account", "connection", "provider", "model", "count", "availability", "metadata"} {
		bad := record
		switch change {
		case "execution":
			bad.ExecutionID = domain.NewID()
		case "session":
			bad.SessionID = seedSearch(t, s, "other", domain.NotArchived).session
		case "turn":
			bad.TurnID = string(domain.NewID())
		case "account":
			bad.AccountID = domain.NewID()
		case "connection":
			bad.ConnectionID = domain.NewID()
		case "provider":
			bad.ProviderID = domain.NewID()
		case "model":
			bad.ModelID = domain.NewID()
		case "count":
			counts := *bad.Usage.Counts
			value := int64(15)
			counts.Total = &value
			bad.Usage.Counts = &counts
		case "availability":
			bad.Usage.Counts = nil
		case "metadata":
			bad.Usage.CostEvidence = domain.UsageCostUnspecified
		}
		_, _, err := writeResponse(s, id, bad)
		assertCode(t, err, domain.RecoveryRequired)
	}
	// A new observation UUID cannot disguise a response reused by another turn.
	bad := record
	bad.TurnID = string(domain.NewID())
	_, _, err = writeResponse(s, domain.NewID(), bad)
	assertCode(t, err, domain.RecoveryRequired)
	var count int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM response_usage").Scan(&count); err != nil || count != 1 {
		t.Fatal("conflicts wrote extra usage", err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.remove-session", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.SessionKind, f.session, 1) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResponseUsage(ctx, id); err == nil {
		t.Fatal("deleted session retained derived usage")
	}
}

func TestResponseUsageRollbackAndMissingCounts(t *testing.T) {
	s, _ := openTest(t)
	record := responseRecord(seedSearch(t, s, "source", domain.NotArchived))
	record.Usage.Counts = nil
	id := domain.NewID()
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.rollback", nil, func(tx *Tx) (any, error) {
		if _, _, err := tx.PutResponseUsage(id, record); err != nil {
			return nil, err
		}
		return nil, domain.Fail(domain.Conflict, "rollback", "retry")
	})
	assertCode(t, err, domain.Conflict)
	if _, err = s.ResponseUsage(context.Background(), id); err == nil {
		t.Fatal("failed publication retained usage")
	}
	if _, _, err = writeResponse(s, id, record); err != nil {
		t.Fatal(err)
	}
	value, err := s.ResponseUsage(context.Background(), id)
	if err != nil || value.Record.Usage.Counts != nil {
		t.Fatal("missing usage invented zero", err)
	}
}
