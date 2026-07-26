package reliability

import "testing"

func TestBillingPortalSessionAuditTypeIsDistinctAndValid(t *testing.T) {
	t.Parallel()
	if AuditBillingPortalSessionCreated == AuditCheckoutCreated {
		t.Fatal("billing portal session and checkout audit types must be distinct")
	}
	if !validAuditType(AuditBillingPortalSessionCreated) {
		t.Fatal("billing portal session audit type is not allowlisted")
	}
}

func TestReservationExpirationAuditTypeIsDistinctAndValid(t *testing.T) {
	t.Parallel()
	if AuditReservationExpired == AuditReservationReleased {
		t.Fatal("reservation expiration and release audit types must be distinct")
	}
	if !validAuditType(AuditReservationExpired) {
		t.Fatal("reservation expiration audit type is not allowlisted")
	}
}
