// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func historyReceiptCounter(t *testing.T, f *continuationFixture) func() int {
	t.Helper()
	path := filepath.ToSlash(filepath.Join(f.service.Store.Root(), "state.sqlite"))
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return func() int {
		t.Helper()
		var count int
		if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM receipts").Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
}

func TestHistoryObservationSkipsUnchangedReceiptsAndNotifications(t *testing.T) {
	f, _, token := accountSwitchFixture(t, "", false)
	ctx := context.Background()
	lease, err := f.service.executionAuthority.Acquire(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	count := historyReceiptCounter(t, f)
	for _, mode := range []domain.NativeHistoryMode{domain.FullNativeHistory, domain.AccountBoundHistory} {
		before, receipts := f.refresh(t), count()
		if err := lease.ObserveHistory(ctx, mode == domain.AccountBoundHistory); err != nil {
			t.Fatal(err)
		}
		after := f.refresh(t)
		if after.Revision != before.Revision+1 || count() != receipts+1 {
			t.Fatal("new history evidence was not durably recorded")
		}
		changed := f.service.Store.Changed()
		receipts = count()
		var wg sync.WaitGroup
		results := make(chan error, 32)
		for i := range 32 {
			wg.Go(func() {
				// Once account-bound, both later full-history requests and
				// repeated account-bound requests must preserve the sticky mode.
				results <- lease.ObserveHistory(ctx, mode == domain.AccountBoundHistory && i%2 == 0)
			})
		}
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatal(err)
			}
		}
		select {
		case <-changed:
			t.Fatal("unchanged history observations woke store watchers")
		default:
		}
		current := f.refresh(t)
		session, err := store.Decode[domain.Session](current)
		if err != nil || current.Revision != after.Revision || count() != receipts || session.CurrentNativeHistory != mode || session.Execution.NativeHistory != mode {
			t.Fatal("unchanged observations wrote receipts or changed sticky history", err)
		}
	}
	f.finish(t, domain.ExecutionStopped)
	receipts := count()
	if err := lease.ObserveHistory(ctx, false); err == nil || count() != receipts {
		t.Fatal("known history bypassed revoked execution authority")
	}
}

func TestHistoryObservationRejectsAccountBoundRequestsAfterSwitch(t *testing.T) {
	f, b, _ := accountSwitchFixture(t, domain.FullNativeHistory, true)
	ctx := context.Background()
	if _, err := sessionClient(f.accountFixture).SwitchSessionAccount(ctx, ownerRequest(f.identity, switchRequest(f, b, t))); err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, "B full history", domain.ExecuteMode)
	f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
	f.claim(t)
	lease, err := f.service.executionAuthority.Acquire(ctx, f.grant(t))
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if err := lease.ObserveHistory(ctx, false); err != nil {
		t.Fatal(err)
	}
	count := historyReceiptCounter(t, f)
	before, receipts := f.refresh(t), count()
	if err := lease.ObserveHistory(ctx, true); err == nil {
		t.Fatal("switched account accepted remote history")
	}
	if err := lease.ObserveHistory(ctx, false); err != nil {
		t.Fatal(err)
	}
	if f.refresh(t).Revision != before.Revision || count() != receipts {
		t.Fatal("rejected or unchanged switched observations wrote state")
	}
}

func TestHistoryObservationConcurrentTransitionsRetainOnlyChangedReceipts(t *testing.T) {
	f, _, token := accountSwitchFixture(t, "", false)
	ctx := context.Background()
	lease, err := f.service.executionAuthority.Acquire(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	count := historyReceiptCounter(t, f)
	before, receipts := f.refresh(t), count()
	start := make(chan struct{})
	results := make(chan error, 32)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			<-start
			results <- lease.ObserveHistory(ctx, i%2 == 0)
		})
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	after := f.refresh(t)
	session, err := store.Decode[domain.Session](after)
	transitions := after.Revision - before.Revision
	if err != nil || transitions < 1 || transitions > 2 || count()-receipts != int(transitions) || session.CurrentNativeHistory != domain.AccountBoundHistory || session.Execution.NativeHistory != domain.AccountBoundHistory {
		t.Fatal("concurrent observations wrote no-op receipts or weakened sticky history", err)
	}
}
