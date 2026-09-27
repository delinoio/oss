package grok

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestStoppedTextObservationRetainsIndependentTerminalAndContent(t *testing.T) {
	for _, mode := range []string{"stop-valid", "stop-completion-race"} {
		t.Run(mode, func(t *testing.T) {
			config, _ := fixtureAPIConfig(t, mode)
			config.Model = turnFixtureModel
			api, err := openAPI(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			owned := &OwnedAPI{connection: api}
			creation, product, input, stop := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
			if _, err := api.Create(context.Background(), creation, product, func(context.Context, CreationClaim) error { return nil }); err != nil {
				t.Fatal(err)
			}
			if _, err := owned.ObserveStoppedText(context.Background()); err == nil {
				t.Fatal("unstarted Stop acquired comparison")
			}
			var chunks []string
			output := sha256.New()
			_, runErr := api.RunText(context.Background(), input, "Original input.", func(context.Context, InputClaim) error { return nil }, func(ctx context.Context, value InputObservation) error {
				switch value.Kind {
				case InputText:
					digest, err := TextChunkDigest(*value.Chunk)
					if err != nil {
						t.Fatal(err)
					}
					chunks = append(chunks, digest)
					_, _ = output.Write([]byte(value.Chunk.Update.Content.Text))
					if _, err := api.StopText(ctx, stop, func(context.Context, StopClaim) error { return nil }); err != nil {
						t.Fatal(err)
					}
					value.Chunk.Update.Content.Text = "changed by callback"
				case InputCompleted:
					value.Result.Meta.Model = "changed by callback"
					value.Result.Meta.Usage.Models[0] = 'x'
				case StopSettled:
					value.Stop.Claim.RequestID = domain.NewID()
					if value.Interruption != nil {
						value.Interruption.Meta.Model = "changed by callback"
					}
				}
				return nil
			})
			if mode == "stop-valid" && runErr == nil || mode == "stop-completion-race" && runErr != nil {
				t.Fatal("original terminal outcome changed", runErr)
			}
			original, err := owned.ObserveStoppedText(context.Background())
			inputDigest, _ := TextInputClaimDigest(turnFixtureSession, "Original input.")
			if err != nil || original.Validate(config.Model) != nil || original.CreationRequestID != creation || original.Stop.Claim.RequestID != stop || original.Stop.Claim.InputRequestID != input || original.InputDigest != inputDigest || !reflect.DeepEqual(original.ChunkDigests, chunks) || original.OutputDigest != hex.EncodeToString(output.Sum(nil)) {
				t.Fatal("caller modified original Stop facts", err)
			}
			encoded, _ := json.Marshal(original)
			for name, mutate := range map[string]func(*StoppedTextObservation){
				"missing delivery": func(v *StoppedTextObservation) { v.Stop.Delivered = false },
				"missing cleanup":  func(v *StoppedTextObservation) { v.Stop.CleanupJoined = false },
				"foreign category": func(v *StoppedTextObservation) { v.Stop.Category = "foreign" },
				"reused creation":  func(v *StoppedTextObservation) { v.CreationRequestID = v.Stop.Claim.RequestID },
				"no chunks":        func(v *StoppedTextObservation) { v.ChunkDigests = nil },
				"invalid digest":   func(v *StoppedTextObservation) { v.OutputDigest = "00" },
				"both variants": func(v *StoppedTextObservation) {
					v.Completed = &TextTerminal{}
					v.Interrupted = &InterruptedTextTerminal{}
				},
				"changed null": func(v *StoppedTextObservation) {
					if v.Interrupted != nil {
						v.Interrupted.Prompt.AgentResult[0] = 'x'
					} else {
						v.Completed.Prompt.AgentResult[0] = 'x'
					}
				},
			} {
				t.Run(name, func(t *testing.T) {
					copy, _ := owned.ObserveStoppedText(context.Background())
					mutate(&copy)
					if copy.Validate(config.Model) == nil {
						t.Fatal("invalid copied Stop evidence accepted")
					}
					unchanged, err := owned.ObserveStoppedText(context.Background())
					raw, _ := json.Marshal(unchanged)
					if err != nil || string(raw) != string(encoded) {
						t.Fatal("read-only copy changed original evidence", err)
					}
				})
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := owned.ObserveStoppedText(canceled); err == nil {
				t.Fatal("canceled inspection accepted")
			}
			if err := os.WriteFile(api.profile.path, []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := owned.ObserveStoppedText(context.Background()); err == nil {
				t.Fatal("changed native configuration accepted")
			}
		})
	}
}
