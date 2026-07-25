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
