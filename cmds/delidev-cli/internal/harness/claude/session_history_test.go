package claude

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSessionHistoryCapturesOriginalManualEventsBeforeConsumerMutation(t *testing.T) {
	for _, failed := range []bool{false, true} {
		core, _ := lifecycleFixture(t)
		ledger := &sessionHistory{}
		prior := lifecycleReplay(t, core)
		if err := ledger.observe(LifecycleObservation{SessionID: core.session, Native: &prior}); err != nil {
			t.Fatal(err)
		}
		core.input = domain.NewID()
		binding := &manualCompactionBinding{core: core}
		for _, event := range manualCompactionEvents(t, core, failed) {
			observed, err := binding.observe(event)
			if err != nil {
				t.Fatal(err)
			}
			if err := ledger.observe(observed); err != nil {
				t.Fatal(err)
			}
			if observed.Native != nil {
				clear(observed.Native.Body)
			}
			if observed.CompactResult != nil {
				observed.CompactResult.Status = "changed"
			}
		}
		if len(ledger.messages) != 1 || len(ledger.actions) != 1 || ledger.echo != nil || ledger.output != nil || ledger.boundary != nil || ledger.action != "" || (ledger.actions[0].Status == CompactFailed) != failed {
			t.Fatal("manual ledger lost independent immutable proof")
		}
		raw, _ := json.Marshal(ledger.actions)
		if bytes.Contains(raw, []byte("Private")) {
			t.Fatal("completed ledger retained private command payload")
		}
	}
}

func TestSessionHistoryRequiresPendingSummaryAndBoundsOriginalProofs(t *testing.T) {
	core, _ := lifecycleFixture(t)
	boundary, summary := compactionSummaryFixture(t, core)
	ledger := &sessionHistory{}
	if err := ledger.observe(LifecycleObservation{Kind: CompactionSummaryObserved, SessionID: core.session, Native: &summary}); err == nil {
		t.Fatal("unbound summary became retained evidence")
	}
	if err := ledger.observe(LifecycleObservation{Kind: CompactionObserved, SessionID: core.session, Native: &boundary}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.observe(LifecycleObservation{Kind: CompactionObserved, SessionID: core.session, Native: &boundary}); err == nil {
		t.Fatal("pending summary was replaced")
	}
	if err := ledger.observe(LifecycleObservation{Kind: CompactionSummaryObserved, SessionID: core.session, Native: &summary}); err != nil {
		t.Fatal(err)
	}
	if len(ledger.compactions) != 1 || len(ledger.messages) != 0 || ledger.boundary != nil {
		t.Fatal("automatic summary became an ordinary input")
	}
	ledger.messages = make([]HistoryMessageProof, maxStreamIdentities)
	event := lifecycleReplay(t, core)
	if err := ledger.observe(LifecycleObservation{SessionID: core.session, Native: &event}); err == nil {
		t.Fatal("unbounded retained message proofs")
	}
	child := lifecycleChange(t, event, "parent_tool_use_id", "original-child-tool")
	if err := ledger.observe(LifecycleObservation{SessionID: core.session, Native: &child}); err != nil {
		t.Fatal("child was relabeled as root")
	}
	if len(ledger.messages) != maxStreamIdentities {
		t.Fatal("child altered root ledger")
	}
	if err := ledger.observe(LifecycleObservation{ActionID: domain.NewID(), SessionID: core.session, Native: &StreamEvent{Kind: NativeRequest}}); err != nil {
		t.Fatal("non-native callback was fabricated into history")
	}
}
