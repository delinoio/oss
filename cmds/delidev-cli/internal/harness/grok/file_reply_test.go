package grok

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type fixtureFileRow struct {
	Kind   nativewire.EventKind `json:"kind"`
	Method string               `json:"method"`
	Params json.RawMessage      `json:"params"`
}

func fixtureFileRows() []fixtureFileRow {
	var rows []fixtureFileRow
	_ = json.Unmarshal(writeToolFixture, &rows)
	return rows
}

func fixtureNotify(method string, raw []byte) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "method": method, "params": json.RawMessage(raw)})
}

func fixtureResponseCounters() {
	var rows []fixtureFileRow
	_ = json.Unmarshal(passiveFixture, &rows)
	for _, row := range rows {
		var v struct {
			Update struct {
				Kind string `json:"sessionUpdate"`
			} `json:"update"`
		}
		_ = json.Unmarshal(row.Params, &v)
		if v.Update.Kind == "response_completed" {
			fixtureNotify(row.Method, row.Params)
		}
	}
}

func fixtureWriteInput(workspace, mode string, request domain.ID, input string) {
	fixtureNotify("_x.ai/queue/changed", queueFixture(input, 0))
	fixtureNotify("_x.ai/queue/changed", queueFixture(input, 1))
	for i, row := range fixtureFileRows() {
		if i == 1 {
			fixtureResponseCounters()
		}
		if row.Kind == nativewire.ServerRequest {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": "original-permission", "method": row.Method, "params": row.Params})
			if mode == "write-unclaimed-resolution" {
				fixtureFileTerminal(workspace, mode, request, false)
			}
			return
		}
		fixtureNotify(row.Method, row.Params)
	}
}

func fixtureFileReply(root, workspace, mode string, request domain.ID, raw []byte) {
	var response struct {
		JSONRPC string               `json:"jsonrpc"`
		ID      string               `json:"id"`
		Result  filePermissionAnswer `json:"result"`
	}
	if decode(raw, &response) != nil || response.JSONRPC != "2.0" || response.ID != "original-permission" || response.Result.Outcome.Outcome != "selected" {
		os.Exit(70)
	}
	if _, err := fileAnswer(response.Result.Outcome.Option); err != nil {
		os.Exit(71)
	}
	_ = os.WriteFile(filepath.Join(root, "tmp", "reply-started"), []byte("started"), 0600)
	if mode == "write-exit-after-reply" {
		os.Exit(0)
	}
	fixtureFileTerminal(workspace, mode, request, response.Result.Outcome.Option == RejectFileOnce)
}

func fixtureFileTerminal(workspace, mode string, request domain.ID, rejected bool) {
	rows := fixtureFileRows()
	resolution, completed := rows[len(rows)-2], rows[len(rows)-1]
	if mode != "write-missing-resolution" {
		fixtureNotify(resolution.Method, resolution.Params)
	}
	if rejected && mode != "write-completed-after-reject" || mode == "write-failed-after-allow" {
		completed.Params = rejectedFileFailedFixture
	}
	if mode == "write-foreign-tool" {
		completed.Params = []byte(strings.ReplaceAll(string(completed.Params), "call_delidev_read", "foreign-tool"))
	}
	if mode != "write-missing-tool" {
		fixtureNotify(completed.Method, completed.Params)
	}
	var result, turn, prompt []byte
	if rejected {
		result, turn, prompt = rejectedFileResultFixture, rejectedFileTurnFixture, rejectedFileCompletedFixture
		if mode == "write-rejection-conflict" {
			prompt = []byte(strings.ReplaceAll(string(prompt), "User rejected the execution", "Changed reason"))
		}
	} else {
		fixtureResponseCounters()
		value, usage := twoResponseResult()
		result, _ = json.Marshal(value)
		ended := fixtureObject(turnCompletedFixture)
		ended["update"].(map[string]any)["usage"] = usage
		ended["_meta"].(map[string]any)["eventId"] = string(turnFixtureSession) + "-9"
		turn, _ = json.Marshal(ended)
		prompt = promptCompletedFixture
	}
	if mode != "write-missing-clear" {
		fixtureNotify("_x.ai/queue/changed", queueFixture("Original input.", 2))
	}
	fixtureNotify("_x.ai/session_notification", turn)
	fixtureNotify("_x.ai/session/prompt_complete", prompt)
	if mode != "write-missing-rpc" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": request, "result": json.RawMessage(result)})
	}
	if mode != "write-missing-idle" {
		fixtureInputTail(workspace, mode, fixtureNotify)
	}
}

func TestOriginalFileReplyClaimsAndTerminalFaults(t *testing.T) {
	for _, mode := range []string{"write-valid", "write-reject", "write-claim-failure", "write-publication-failure", "write-unclaimed-resolution", "write-missing-resolution", "write-missing-tool", "write-foreign-tool", "write-completed-after-reject", "write-failed-after-allow", "write-rejection-conflict", "write-exit-after-reply", "write-missing-clear", "write-missing-rpc", "write-missing-idle"} {
		t.Run(mode, func(t *testing.T) {
			config, _ := fixtureAPIConfig(t, mode)
			config.Model = turnFixtureModel
			api, err := openAPI(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			creation, product, input := domain.NewID(), domain.NewID(), domain.NewID()
			if _, err := api.Create(context.Background(), creation, product, func(context.Context, CreationClaim) error { return nil }); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var timer *time.Timer
			timed := make(chan struct{})
			defer func() {
				if timer != nil && !timer.Stop() {
					<-timed
				}
			}()
			var arrival domain.ID
			claims := 0
			completed, rejected := false, false
			_, err = api.RunFileTools(ctx, input, "Original input.", func(context.Context, InputClaim) error { return nil }, func(callback context.Context, v InputObservation) error {
				if v.Permission != nil {
					arrival = v.Permission.ArrivalID
					if mode == "write-unclaimed-resolution" {
						return nil
					}
					decision := AllowFileOnce
					if mode == "write-reject" || mode == "write-completed-after-reject" || mode == "write-rejection-conflict" || mode == "write-missing-idle" {
						decision = RejectFileOnce
					}
					for _, id := range []domain.ID{creation, product, input, config.Probe.Process.OwnerID, arrival, domain.ID(turnFixtureSession)} {
						if _, e := api.ReplyFilePermission(callback, id, arrival, decision, func(context.Context, FilePermissionClaim) error { t.Error("reused ID claimed reply"); return nil }); e == nil {
							t.Fatal("prior identity acquired response")
						}
					}
					if _, e := api.ReplyFilePermission(callback, domain.NewID(), arrival, "allow-edits-session", func(context.Context, FilePermissionClaim) error { t.Error("session policy claimed"); return nil }); e == nil {
						t.Fatal("unsupported remembered policy sent")
					}
					delivered, e := api.ReplyFilePermission(callback, domain.NewID(), arrival, decision, func(_ context.Context, c FilePermissionClaim) error {
						claims++
						if c.Validate() != nil || c.InputRequestID != input || c.NativePromptID != turnFixturePrompt {
							t.Error("foreign reply claim")
						}
						if mode == "write-claim-failure" {
							return context.Canceled
						}
						return nil
					})
					if mode == "write-claim-failure" {
						if e == nil || delivered.Claimed || delivered.Attempted {
							t.Error("failed persistence sent reply")
						}
						return e
					}
					if e != nil {
						return e
					}
					if !delivered.Claimed || !delivered.Delivered || delivered.Resolved || delivered.ToolPhase != "" {
						t.Fatal("delivery substituted native acceptance")
					}
					if strings.HasPrefix(mode, "write-missing-") {
						timer = time.AfterFunc(time.Second, func() { defer close(timed); cancel() })
					}
					if mode == "write-publication-failure" {
						return context.Canceled
					}
				}
				if v.Kind == InputCompleted {
					completed = true
				}
				if v.Kind == InputPermissionRejected {
					rejected = true
				}
				return nil
			})
			if mode == "write-valid" {
				if err != nil || !completed || rejected {
					t.Fatal("original Write failed", err)
				}
			} else if mode == "write-reject" {
				if err == nil || domain.SafeError(err).Code != domain.Canceled || completed || !rejected {
					t.Fatal("original rejection failed", err)
				}
			} else if err == nil || completed || rejected {
				t.Fatal("uncertain Write completed", err)
			}
			if mode != "write-unclaimed-resolution" && claims != 1 {
				t.Fatal("reply claim count changed", claims)
			}
			if mode == "write-claim-failure" {
				if _, e := os.Stat(filepath.Join(filepath.Dir(config.Probe.Home), "tmp", "reply-started")); !os.IsNotExist(e) {
					t.Fatal("failed claim delivered a response")
				}
			}
			if _, e := api.ReplyFilePermission(context.Background(), domain.NewID(), arrival, AllowFileOnce, func(context.Context, FilePermissionClaim) error { t.Error("reply replay claimed"); return nil }); e == nil {
				t.Fatal("original reply replayed")
			}
			if api.completedText != nil {
				t.Fatal("Write acquired text-history ownership")
			}
		})
	}
}

func TestOriginalFileReplyIsIndependentOfBlockedPublication(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "write-valid")
	config.Model = turnFixtureModel
	api, err := openAPI(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	if _, err := api.Create(context.Background(), domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	offers := make(chan FilePermissionOffer, 1)
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := api.RunFileTools(ctx, domain.NewID(), "Original input.", func(context.Context, InputClaim) error { return nil }, func(callback context.Context, v InputObservation) error {
			if v.Permission != nil {
				offers <- *v.Permission
				select {
				case <-release:
				case <-callback.Done():
					return callback.Err()
				}
			}
			return nil
		})
		finished <- err
	}()
	var offer FilePermissionOffer
	select {
	case offer = <-offers:
	case <-ctx.Done():
		t.Fatal("proposal not published")
	}
	var wg sync.WaitGroup
	results := make(chan FilePermissionDelivery, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, _ := api.ReplyFilePermission(ctx, domain.NewID(), offer.ArrivalID, AllowFileOnce, func(context.Context, FilePermissionClaim) error { return nil })
			results <- v
		}()
	}
	wg.Wait()
	close(results)
	delivered := 0
	for v := range results {
		if v.Delivered {
			delivered++
			if v.Resolved {
				t.Fatal("blocked publication granted native resolution")
			}
		}
	}
	close(release)
	if err := <-finished; err != nil || delivered != 1 {
		t.Fatal("concurrent original responses were not single-use", delivered, err)
	}
}

func TestOriginalFileReplyClaimJoinsNativeLifetimeLoss(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "write-valid")
	config.Model = turnFixtureModel
	api, err := openAPI(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	if _, err := api.Create(context.Background(), domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	offers := make(chan FilePermissionOffer, 1)
	inputDone := make(chan error, 1)
	go func() {
		_, err := api.RunFileTools(ctx, domain.NewID(), "Original input.", func(context.Context, InputClaim) error { return nil }, func(callback context.Context, v InputObservation) error {
			if v.Permission != nil {
				offers <- *v.Permission
				<-callback.Done()
				return callback.Err()
			}
			return nil
		})
		inputDone <- err
	}()
	var offer FilePermissionOffer
	select {
	case offer = <-offers:
	case <-ctx.Done():
		t.Fatal("original permission not observed")
	}
	claimEntered := make(chan struct{})
	replyDone := make(chan error, 1)
	go func() {
		_, err := api.ReplyFilePermission(ctx, domain.NewID(), offer.ArrivalID, AllowFileOnce, func(record context.Context, _ FilePermissionClaim) error {
			close(claimEntered)
			<-record.Done()
			return record.Err()
		})
		replyDone <- err
	}()
	select {
	case <-claimEntered:
	case <-ctx.Done():
		t.Fatal("original reply claim not entered")
	}
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-replyDone:
		if err == nil {
			t.Fatal("failed record sent original response")
		}
	case <-ctx.Done():
		t.Fatal("native lifetime did not cancel original claim")
	}
	select {
	case err := <-inputDone:
		if err == nil {
			t.Fatal("native lifetime loss completed input")
		}
	case <-ctx.Done():
		t.Fatal("input did not join response and publication")
	}
	v, err := api.textControl().inspectFileReply(offer.ArrivalID)
	if err != nil || v.Claimed || v.Attempted || v.Delivered || v.ProblemCode != domain.RecoveryRequired {
		t.Fatal("native lifetime loss fabricated reply acceptance", err)
	}
	if _, err := api.ReplyFilePermission(ctx, domain.NewID(), offer.ArrivalID, AllowFileOnce, func(context.Context, FilePermissionClaim) error { t.Error("lost original claim retried"); return nil }); err == nil {
		t.Fatal("lost original reply reopened")
	}
}
