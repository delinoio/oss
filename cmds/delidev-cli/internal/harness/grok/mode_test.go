package grok

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

// Original pinned native observation; only the private session UUID changed.
//
//go:embed testdata/initial-plan-mode.json
var initialPlanFixture []byte

func fixtureInitialPlan(root, mode string, request domain.ID, raw []byte) {
	var params modeParams
	if decode(raw, &params) != nil || params.Session != turnFixtureSession || params.Mode != NativePlanMode {
		os.Exit(74)
	}
	_ = os.WriteFile(filepath.Join(root, "tmp", "mode-started"), []byte("started"), 0600)
	write := func(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }
	response := func() {
		result := any(map[string]any{})
		if mode == "mode-result-extension" {
			result = map[string]any{"accepted": true}
		}
		if mode == "mode-result-null" {
			result = nil
		}
		write(map[string]any{"jsonrpc": "2.0", "id": request, "result": result})
	}
	if mode == "mode-native-error" {
		write(map[string]any{"jsonrpc": "2.0", "id": request, "error": map[string]any{"code": -32603, "message": "private-mode-error"}})
		return
	}
	if mode == "mode-exit" {
		os.Exit(0)
	}
	if mode == "mode-rpc-first" {
		response()
	}
	if mode != "mode-missing-event" {
		observation := fixtureObject(initialPlanFixture)
		if mode == "mode-foreign" {
			observation["sessionId"] = domain.NewID()
		}
		if mode == "mode-wrong" {
			observation["update"].(map[string]any)["currentModeId"] = NativeDefaultMode
		}
		if mode == "mode-command-prefix" || mode == "mode-reused-event" {
			var rows []fixtureFileRow
			_ = json.Unmarshal(passiveFixture, &rows)
			for _, row := range rows {
				value := fixtureObject(row.Params)
				if value["update"].(map[string]any)["sessionUpdate"] == "available_commands_update" {
					value["_meta"].(map[string]any)["eventId"] = string(turnFixtureSession) + "-1"
					value["_meta"].(map[string]any)["promptId"] = nil
					for _, key := range []string{"promptId", "turnStartMs", "streamStartMs"} {
						delete(value["_meta"].(map[string]any), key)
					}
					body, _ := json.Marshal(value)
					fixtureNotify(row.Method, body)
					break
				}
			}
			if mode == "mode-reused-event" {
				observation["_meta"].(map[string]any)["eventId"] = string(turnFixtureSession) + "-1"
			}
		}
		body, _ := json.Marshal(observation)
		fixtureNotify("session/update", body)
		if mode == "mode-duplicate" {
			fixtureNotify("session/update", body)
		}
	}
	if mode != "mode-rpc-first" && mode != "mode-missing-rpc" {
		response()
	}
}

func TestInitialPlanRequiresOriginalClaimAckAndMode(t *testing.T) {
	for _, mode := range []string{"mode-valid", "mode-rpc-first", "mode-command-prefix", "mode-missing-event", "mode-missing-rpc", "mode-native-error", "mode-result-extension", "mode-result-null", "mode-foreign", "mode-wrong", "mode-reused-event", "mode-exit", "mode-claim-failure", "mode-bind-failure", "mode-duplicate"} {
		t.Run(mode, func(t *testing.T) {
			config, logs := fixtureAPIConfig(t, mode)
			config.Mode, config.Model = domain.PlanMode, turnFixtureModel
			api, err := openAPI(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			if _, err := api.SelectPlan(context.Background(), domain.NewID(), func(context.Context, ModeClaim) error { t.Error("selection preceded creation"); return nil }); err == nil {
				t.Fatal("selection preceded original creation")
			}
			if _, err := api.Create(context.Background(), domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
				t.Fatal(err)
			}
			var claims []ModeClaim
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var timer *time.Timer
			defer func() {
				if timer != nil {
					timer.Stop()
				}
			}()
			request := domain.NewID()
			binding, err := api.SelectPlan(ctx, request, func(_ context.Context, c ModeClaim) error {
				if c.Validate() != nil || c.RequestID != request {
					t.Fatal("invalid original mode claim")
				}
				_, stat := os.Stat(filepath.Join(filepath.Dir(config.Probe.Home), "tmp", "mode-started"))
				if c.Phase == ClaimMode && !os.IsNotExist(stat) || c.Phase == BindMode && stat != nil {
					t.Fatal("mode claim lost its native boundary")
				}
				claims = append(claims, c)
				if c.Phase == ClaimMode && (mode == "mode-missing-event" || mode == "mode-missing-rpc") {
					timer = time.AfterFunc(500*time.Millisecond, cancel)
				}
				if mode == "mode-claim-failure" && c.Phase == ClaimMode || mode == "mode-bind-failure" && c.Phase == BindMode {
					return sessionUncertain()
				}
				return nil
			})
			valid := mode == "mode-valid" || mode == "mode-rpc-first" || mode == "mode-command-prefix" || mode == "mode-duplicate"
			if valid {
				if err != nil || len(claims) != 2 || binding != claims[1] || binding.Phase != BindMode {
					t.Fatal("original mode did not bind", err)
				}
				// Returned metadata is a copy; callers cannot alter the live binding.
				binding.EventID = "changed"
				if api.modeBinding.EventID == binding.EventID {
					t.Fatal("mode binding was aliased")
				}
			} else if err == nil || api.modeBinding != nil {
				t.Fatal("incomplete selection granted input")
			}
			if _, err := api.SelectPlan(context.Background(), domain.NewID(), func(context.Context, ModeClaim) error { t.Error("selection replay claimed"); return nil }); err == nil {
				t.Fatal("selection replayed")
			}
			if _, err := api.RunText(context.Background(), domain.NewID(), "Original input.", func(context.Context, InputClaim) error { t.Error("Plan downgraded input"); return nil }, func(context.Context, InputObservation) error { return nil }); err == nil {
				t.Fatal("Plan borrowed Execute input")
			}
			if mode == "mode-duplicate" {
				_, err := api.runInput(context.Background(), domain.NewID(), "Original input.", func(context.Context, InputClaim) error { return nil }, func(context.Context, InputObservation) error { t.Error("duplicate mode published input"); return nil }, planQuestionInput)
				if err == nil {
					t.Fatal("duplicate mode event was discarded")
				}
			}
			if err := api.Close(); err != nil {
				t.Fatal(err)
			}
			if err := process.ReconcileOwner(config.Probe.Process.Directory, config.Probe.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{config.Token, config.Workspace, "private-mode-error"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("mode diagnostics exposed private data")
				}
			}
		})
	}
}

func TestInitialPlanSelectionCannotPrecedeOrReplaceOriginalAuthority(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "mode-valid")
	config.Mode = domain.PlanMode
	api, err := openAPI(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	if _, err := api.Create(context.Background(), domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
		t.Fatal(err)
	}
	inputClaim := func(context.Context, InputClaim) error { t.Error("input claimed without mode authority"); return nil }
	emit := func(context.Context, InputObservation) error {
		t.Error("input published without mode authority")
		return nil
	}
	if _, err := api.runInput(context.Background(), domain.NewID(), "Original input.", inputClaim, emit, planQuestionInput); err == nil {
		t.Fatal("Plan input preceded mode selection")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := api.SelectPlan(ctx, domain.NewID(), func(ctx context.Context, c ModeClaim) error {
			once.Do(func() { close(entered) })
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		done <- err
	}()
	<-entered
	blocked, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	if _, err := api.runInput(blocked, domain.NewID(), "Original input.", inputClaim, emit, planQuestionInput); err == nil {
		t.Fatal("input bypassed pending mode claim")
	}
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("native loss granted selection")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("mode claim did not join native lifetime")
	}
	close(release)
	if api.modeBinding != nil {
		t.Fatal("lost native mode created binding")
	}
}

func TestPlanQuestionModeIsOriginalAndImmutable(t *testing.T) {
	for _, mode := range []NativeMode{NativeDefaultMode, NativePlanMode} {
		o, _ := newQuestionObserverForMode(turnFixtureSession, turnFixturePrompt, mode)
		for _, event := range questionEvents(t) {
			if event.Kind == nativewire.ServerRequest {
				before := o.tools[eventToolID(event)]
				v := fixtureObject(event.Params)
				other := NativeDefaultMode
				if mode == NativeDefaultMode {
					other = NativePlanMode
				}
				v["mode"] = other
				event.Params, _ = json.Marshal(v)
				if _, err := o.observe(event); err == nil {
					t.Fatal("foreign question mode accepted")
				}
				if !reflect.DeepEqual(before, o.tools[eventToolID(event)]) {
					t.Fatal("foreign mode changed original question")
				}
				v["mode"] = mode
				event.Params, _ = json.Marshal(v)
			}
			if _, err := o.observe(event); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := newQuestionObserverForMode(turnFixtureSession, turnFixturePrompt, "unknown"); err == nil {
		t.Fatal("unknown native mode accepted")
	}
}

func eventToolID(event nativewire.Event) string {
	var v struct {
		ID string `json:"toolCallId"`
	}
	_ = json.Unmarshal(event.Params, &v)
	return v.ID
}

func TestInitialPlanClosedNativeModeSchema(t *testing.T) {
	event := nativewire.Event{Kind: nativewire.Notification, Method: "session/update", Params: initialPlanFixture}
	if _, err := parseModeObservation(event, turnFixtureSession); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(map[string]any){
		func(v map[string]any) { v["sessionId"] = domain.NewID() },
		func(v map[string]any) { v["sessionid"] = v["sessionId"]; delete(v, "sessionId") },
		func(v map[string]any) { v["update"].(map[string]any)["currentModeId"] = "unknown" },
		func(v map[string]any) { v["update"].(map[string]any)["promptId"] = turnFixturePrompt },
		func(v map[string]any) { v["_meta"].(map[string]any)["eventId"] = string(domain.NewID()) + "-2" },
		func(v map[string]any) { v["_meta"].(map[string]any)["agentTimestampMs"] = nil },
		func(v map[string]any) { v["_meta"].(map[string]any)["agentTimestampMs"] = -1 },
		func(v map[string]any) { v["_meta"].(map[string]any)["agentTimestampMs"] = 0 },
		func(v map[string]any) { v["_meta"].(map[string]any)["agentTimestampMs"] = 253402300800000 },
	} {
		v := fixtureObject(initialPlanFixture)
		mutate(v)
		changed := event
		changed.Params, _ = json.Marshal(v)
		if _, err := parseModeObservation(changed, turnFixtureSession); err == nil {
			t.Fatal("malformed mode observation accepted")
		}
	}
	changed := event
	changed.Params = append([]byte(`{"sessionId":"`+string(turnFixtureSession)+`",`), initialPlanFixture[1:]...)
	if _, err := parseModeObservation(changed, turnFixtureSession); err == nil {
		t.Fatal("duplicate mode keys accepted")
	}
	if !bytes.Equal(event.Params, initialPlanFixture) {
		t.Fatal("mode parser changed original native bytes")
	}
}
