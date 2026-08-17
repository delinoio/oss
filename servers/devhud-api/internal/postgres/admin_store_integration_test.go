//go:build integration

package postgres

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"github.com/delinoio/oss/servers/devhud-api/internal/idgen"
)

func TestAdministratorSearchRaceAndAuditIntegrity(t *testing.T) {
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	ctx, _, store := newIntegrationStore(t, now)
	actor := provisionUploadUser(t, ctx, store, "administrator")
	target, err := store.ProvisionUser(ctx, domain.Identity{
		Issuer: "https://issuer.example", Subject: "target-subject",
		DisplayName: "  E\u0301lodie  ", Email: "Target@Example.com",
		Fingerprint: []byte("01234567890123456789012345678901"),
	})
	if err != nil {
		t.Fatal(err)
	}
	users, err := store.ListUsers(ctx, normalizeSearch("  ÉLO  "), nil, 50)
	if err != nil || len(users.Users) != 1 || users.Users[0].ID != target.ID {
		t.Fatalf("normalized users=%+v err=%v", users, err)
	}

	ids := idgen.UUIDv7{}
	type result struct{ err error }
	results := make(chan result, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			eventID, idErr := ids.New()
			if idErr != nil {
				results <- result{err: idErr}
				return
			}
			correlationID, idErr := ids.New()
			if idErr != nil {
				results <- result{err: idErr}
				return
			}
			event := domain.AuditEvent{
				ID: eventID, CorrelationID: correlationID, ActorUserID: &actor.ID, TargetUserID: &target.ID,
				Action: domain.AuditActionUserBlocked, Reason: "Concurrent policy review.",
				CreatedAt: now, ExpiresAt: now.Add(domain.AuditRetention),
			}
			_, mutationErr := store.SetUserBlocked(ctx, actor.ID, target.ID,
				domain.AdministrativeBlockStateUnblocked, domain.AdministrativeBlockStateBlocked, event, now)
			results <- result{err: mutationErr}
		}()
	}
	wait.Wait()
	close(results)
	var accepted, conflicted int
	for outcome := range results {
		var conflict *domain.AdminConflictError
		if outcome.err == nil {
			accepted++
		} else if errors.As(outcome.err, &conflict) {
			conflicted++
		} else {
			t.Fatal(outcome.err)
		}
	}
	if accepted != 1 || conflicted != 1 {
		t.Fatalf("accepted=%d conflicted=%d", accepted, conflicted)
	}
	audits, err := store.ListAuditEvents(ctx, domain.AuditFilters{TargetUserID: target.ID}, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits.Events) != 2 {
		t.Fatalf("audit events=%+v", audits.Events)
	}
	outcomes := map[domain.AuditOutcome]int{}
	for _, event := range audits.Events {
		outcomes[event.Outcome]++
		if event.CorrelationID == "" || event.Reason != "Concurrent policy review." {
			t.Fatalf("unsafe or uncorrelated audit=%+v", event)
		}
	}
	if outcomes[domain.AuditOutcomeAccepted] != 1 || outcomes[domain.AuditOutcomeRejected] != 1 {
		t.Fatalf("outcomes=%v", outcomes)
	}
}
