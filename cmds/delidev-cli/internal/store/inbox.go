package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func (t *Tx) InboxBySource(source domain.InboxSource, id domain.ID) (Record, error) {
	if id.Validate() != nil || (source != domain.InteractionInbox && source != domain.ExecutionTerminalInbox && source != domain.SubscriptionRecoveryInbox && source != domain.OperationalInbox) {
		return Record{}, domain.Fail(domain.InvalidArgument, "Invalid inbox source.", "Use the retained interaction or execution identity.")
	}
	r, err := scan(t.tx.QueryRowContext(t.ctx, "SELECT "+recordColumns+" FROM entities WHERE kind='inbox' AND json_extract(body,'$.source')=? AND json_extract(body,'$.source_id')=?", source, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, domain.Fail(domain.NotFound, "The source has no retained inbox entry.", "Refresh the inbox and inspect the original source.")
	}
	return r, storageError(err)
}

// CreateInboxEntry shares the native publication transaction. Source ownership
// is unique across receipt identities; neither replay nor closure can create a
// replacement entry or reset its independent read state.
func (t *Tx) CreateInboxEntry(session, project domain.ID, value domain.InboxEntry) (Record, error) {
	if err := t.writeAllowed(); err != nil {
		return Record{}, err
	}
	if err := value.Validate(); err != nil {
		return Record{}, err
	}
	if value.ReadState != domain.InboxUnread {
		return Record{}, inboxConflict()
	}
	sr, err := t.Get(domain.SessionKind, session)
	if err != nil {
		return Record{}, err
	}
	if sr.ProjectID != project {
		return Record{}, inboxConflict()
	}
	if value.Source == domain.InteractionInbox {
		r, err := t.Get(domain.InteractionKind, value.SourceID)
		if err != nil {
			return Record{}, err
		}
		interaction, err := Decode[domain.ExecutionInteraction](r)
		if err != nil {
			return Record{}, err
		}
		request := domain.ExecutionInteractionUpdate{ID: r.ID, NativeItemID: interaction.NativeItemID, NativeRequestID: interaction.NativeRequestID, Type: interaction.Type, Questions: interaction.Questions, Approval: interaction.Approval, OpenCode: interaction.OpenCode, Claude: interaction.Claude, Grok: interaction.Grok}
		if r.SessionID != session || r.ProjectID != project || request.Validate(domain.ExecutionInteractionRequested) != nil || interaction.ExecutionID.Validate() != nil || domain.Text(interaction.NativeThreadID, "native thread identity", 1024, true) != nil || domain.Text(interaction.NativeTurnID, "native turn identity", 1024, true) != nil || interaction.FirstSequence == 0 || interaction.LastSequence < interaction.FirstSequence || interaction.LastSequence > domain.MaxExecutionEvents {
			return Record{}, inboxConflict()
		}
		if interaction.Closure != domain.InteractionOpen && interaction.Closure != domain.InteractionNativeClosed && interaction.Closure != domain.InteractionTurnEnded {
			return Record{}, inboxConflict()
		}
	} else {
		r, err := t.Get(domain.JobKind, value.Terminal.JobID)
		if err != nil {
			return Record{}, err
		}
		job, err := Decode[domain.Job](r)
		if err != nil {
			return Record{}, err
		}
		var input domain.ExecutionJobInput
		if r.SessionID != session || r.ProjectID != project || job.Type != domain.ExecuteSessionJob || domain.Decode(job.Input, &input) != nil || input.SessionID != session || input.ExecutionID != value.SourceID || input.InputID != value.Terminal.InputID {
			return Record{}, inboxConflict()
		}
		queued, err := t.Get(domain.QueueKind, value.Terminal.InputID)
		if err != nil {
			return Record{}, err
		}
		q, err := Decode[domain.QueuedInput](queued)
		if err != nil {
			return Record{}, err
		}
		if queued.SessionID != session || queued.ProjectID != project || q.ExecutionID != value.SourceID || q.Delivery != domain.InputAccepted {
			return Record{}, inboxConflict()
		}
	}
	return t.Put(domain.InboxKind, domain.NewID(), 0, session, project, value)
}

// The dedicated owner API supplies authentication and a reference-only receipt.
// Reading a record alone never calls this mutation, and its revision belongs to
// the inbox entry rather than the question or native response control.
func (t *Tx) SetInboxReadState(id domain.ID, revision uint64, state domain.InboxReadState) (Record, error) {
	if err := t.writeAllowed(); err != nil {
		return Record{}, err
	}
	if !state.Valid() || revision == 0 {
		return Record{}, domain.Fail(domain.InvalidArgument, "Invalid inbox read-state mutation.", "Use the inbox entry's current revision and read or unread state.")
	}
	r, err := t.Get(domain.InboxKind, id)
	if err != nil {
		return Record{}, err
	}
	value, err := Decode[domain.InboxEntry](r)
	if err != nil {
		return Record{}, err
	}
	if value.Validate() != nil || r.Revision != revision {
		return Record{}, inboxConflict()
	}
	if value.ReadState == state {
		return r, nil
	}
	value.ReadState = state
	return t.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, value)
}

func inboxConflict() error {
	return domain.Fail(domain.Conflict, "The inbox entry or original source does not match.", "Reload its current read state and original source without changing response or execution authority.")
}

// Legacy versions retain all questions and the most recent execution progress.
// Backfill only that evidence; absent older native history cannot be invented.
// Pages are closed before writes so migration memory is bounded independently
// of total retained history.
func (t *Tx) preserveLegacyInbox() error {
	for _, kind := range []domain.Kind{domain.InteractionKind, domain.SessionKind} {
		after := domain.ID("")
		for {
			rows, err := t.List(Filter{Kind: kind, After: after, Limit: MaxPage})
			if err != nil {
				return err
			}
			for _, r := range rows {
				entry := domain.InboxEntry{Source: domain.InteractionInbox, SourceID: r.ID, ReadState: domain.InboxUnread}
				session := r.SessionID
				if kind == domain.SessionKind {
					value, err := Decode[domain.Session](r)
					if err != nil {
						return inboxMigrationError(err)
					}
					p := value.Execution
					if p == nil || p.Outcome == domain.ExecutionRunning || p.Outcome == domain.ExecutionNotStarted {
						continue
					}
					session = r.ID
					entry.Source, entry.SourceID = domain.ExecutionTerminalInbox, p.ExecutionID
					entry.Terminal = &domain.InboxTerminal{JobID: p.JobID, InputID: p.InputID, NativeThreadID: p.NativeThreadID, NativeTurnID: p.NativeTurnID, Sequence: p.LastSequence, Outcome: p.Outcome}
				}
				if _, err := t.CreateInboxEntry(session, r.ProjectID, entry); err != nil {
					return inboxMigrationError(err)
				}
			}
			if len(rows) < MaxPage {
				break
			}
			after = rows[len(rows)-1].ID
		}
	}
	return nil
}

func inboxMigrationError(err error) error {
	switch domain.SafeError(err).Code {
	case domain.InvalidArgument, domain.Conflict, domain.NotFound:
		return domain.Fail(domain.RecoveryRequired, "Retained inbox sources could not be migrated consistently.", "Preserve the original database and pre-migration backup; reconcile the original source ownership without discarding history.")
	default:
		// Cancellation, disk exhaustion and I/O retain their actionable cause;
		// they are not evidence of contradictory native ownership.
		return err
	}
}

type InboxFilter struct {
	SessionID domain.ID
	ProjectID domain.ID
	Source    domain.InboxSource
	ReadState domain.InboxReadState
	After     domain.ID
	Limit     int
	Epoch     uint64
}

func (f InboxFilter) Validate() error {
	if err := (Filter{Kind: domain.InboxKind, SessionID: f.SessionID, ProjectID: f.ProjectID, After: f.After, Limit: f.Limit}).validate(); err != nil {
		return err
	}
	if f.Source != "" && f.Source != domain.InteractionInbox && f.Source != domain.ExecutionTerminalInbox && f.Source != domain.SubscriptionRecoveryInbox && f.Source != domain.OperationalInbox {
		return domain.Fail(domain.InvalidArgument, "Unknown inbox source filter.", "Select interaction or execution-terminal, or omit the filter.")
	}
	if f.ReadState != "" && !f.ReadState.Valid() {
		return domain.Fail(domain.InvalidArgument, "Unknown inbox read-state filter.", "Select read or unread, or omit the filter.")
	}
	return nil
}

// Callers join current source/session documents inside this same transaction.
// The inbox epoch prevents read-state changes from silently skipping or repeating
// membership across pages; source details remain current for each page.
func (t *Tx) InboxPage(f InboxFilter) ([]Record, bool, uint64, error) {
	if err := f.Validate(); err != nil {
		return nil, false, 0, err
	}
	var epoch uint64
	if err := t.tx.QueryRowContext(t.ctx, "SELECT MAX(COALESCE((SELECT MAX(sequence) FROM events WHERE kind='inbox'),0),COALESCE((SELECT CAST(value AS INTEGER) FROM metadata WHERE key='event_floor'),0))").Scan(&epoch); err != nil {
		return nil, false, 0, storageError(err)
	}
	if f.After != "" && f.Epoch != epoch {
		return nil, false, 0, domain.Fail(domain.CursorExpired, "The inbox changed during pagination.", "Restart inbox pagination to use current entries and read states.")
	}
	query := "SELECT " + recordColumns + " FROM entities WHERE kind='inbox' AND id>?"
	args := []any{f.After}
	for _, part := range []struct {
		column string
		value  string
	}{{"session_id", string(f.SessionID)}, {"project_id", string(f.ProjectID)}, {"json_extract(body,'$.source')", string(f.Source)}, {"json_extract(body,'$.read_state')", string(f.ReadState)}} {
		if part.value != "" {
			query += " AND " + part.column + "=?"
			args = append(args, part.value)
		}
	}
	query += " ORDER BY id LIMIT ?"
	args = append(args, f.Limit+1)
	rows, more, err := t.sessionPage(f.Limit, query, args...)
	return rows, more, epoch, err
}

// Recovery has no session authority. Original observation identity is unique
// through the same durable inbox source index as native execution publication.
func (t *Tx) CreateSubscriptionRecoveryInbox(account, source, connection domain.ID, observed time.Time) (Record, error) {
	if err := t.writeAllowed(); err != nil {
		return Record{}, err
	}
	r, err := t.Get(domain.AccountKind, account)
	if err != nil {
		return Record{}, err
	}
	a, err := Decode[domain.Account](r)
	if err != nil {
		return Record{}, err
	}
	if a.SubscriptionService != domain.SubscriptionChatGPT || a.Connection == nil || a.Connection.ID != connection || a.ConfirmedExhausted || !a.RecoveryNotifications || observed.IsZero() {
		return Record{}, inboxConflict()
	}
	existing, err := t.InboxBySource(domain.SubscriptionRecoveryInbox, source)
	if err == nil {
		return existing, nil
	}
	if domain.SafeError(err).Code != domain.NotFound {
		return Record{}, err
	}
	entry := domain.InboxEntry{Source: domain.SubscriptionRecoveryInbox, SourceID: source, ReadState: domain.InboxUnread, Recovery: &domain.InboxSubscriptionRecovery{AccountID: account, ConnectionID: connection, ObservedAt: observed}}
	if err := entry.Validate(); err != nil {
		return Record{}, err
	}
	return t.Put(domain.InboxKind, domain.NewID(), 0, "", "", entry)
}

// Delete account-scoped recovery entries through ordinary tombstone/event
// publication in the account deletion transaction. Session inboxes are unrelated.
func (t *Tx) deleteAccountRecoveryInbox(account domain.ID) error {
	for {
		records, _, err := t.sessionPage(MaxPage, "SELECT "+recordColumns+" FROM entities WHERE kind='inbox' AND json_extract(body,'$.source')=? AND json_extract(body,'$.recovery.account_id')=? ORDER BY id LIMIT ?", domain.SubscriptionRecoveryInbox, account, MaxPage)
		if err != nil {
			return err
		}
		for _, record := range records {
			entry, err := Decode[domain.InboxEntry](record)
			if err != nil || entry.Validate() != nil || entry.Recovery == nil || entry.Recovery.AccountID != account || record.SessionID != "" || record.ProjectID != "" {
				return inboxConflict()
			}
			if err := t.Delete(domain.InboxKind, record.ID, record.Revision); err != nil {
				return err
			}
		}
		if len(records) < MaxPage {
			return nil
		}
	}
}

// UnreadInboxCount reads every retained source in one transaction. Notification
// preferences, source joins and delivery claims cannot filter this aggregate.
func (t *Tx) UnreadInboxCount() (uint64, error) {
	var count uint64
	err := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM entities WHERE kind='inbox' AND json_extract(body,'$.read_state')=?", domain.InboxUnread).Scan(&count)
	return count, storageError(err)
}
