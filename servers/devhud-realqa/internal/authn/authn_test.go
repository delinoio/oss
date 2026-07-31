package authn

import (
	"reflect"
	"testing"

	"github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1/realqav1connect"
)

func TestStorageRecoveryProceduresRequireHumanBillingAuthority(
	t *testing.T,
) {
	t.Parallel()
	for _, fixture := range []struct {
		procedure string
		feature   string
		forwarded []string
	}{
		{
			realqav1connect.RealQASubmissionServiceRebindSubmissionStorageAuthorizationProcedure,
			"realqa:submissions:write",
			[]string{
				"delibase:billing:read",
				"delibase:billing:write",
			},
		},
		{
			realqav1connect.RealQATrackerServiceDisconnectGitHubConnectionProcedure,
			"realqa:tracker:write",
			[]string{
				"delibase:account:read",
				"delibase:billing:read",
				"delibase:billing:write",
			},
		},
	} {
		feature, forwarded, ok := scopes(fixture.procedure)
		if !ok || feature != fixture.feature ||
			!reflect.DeepEqual(forwarded, fixture.forwarded) {
			t.Fatalf("procedure %q scopes = %q, %#v, %t",
				fixture.procedure, feature, forwarded, ok)
		}
	}
}
