package domain

import "testing"

func reviewerDomainFixture() (RepositoryItem, PullRequestReviewers) {
	item, feedback := feedbackDomainFixture()
	author := FeedbackAuthor{NativeType: "Bot", Kind: RepositoryBot, ID: "199175422", NodeID: "BOT_1", Login: "reviewer-app"}
	for i := range feedback.Entries {
		feedback.Entries[i].Author = &author
	}
	value := PullRequestReviewers{Feedback: feedback, Actors: []PRReviewerIdentity{{Author: author, IdentityAccess: IntegrationAccessAvailable, Identity: &RepositoryActor{ProviderType: "Bot", Kind: RepositoryBot, ID: author.ID, NodeID: author.NodeID, Login: "reviewer-app[bot]"}, Permission: ReviewerPermission{Access: IntegrationAccessAvailable, LegacyPermission: "none", Minimum: PermissionNone, Maximum: PermissionNone}}}, Applications: []FeedbackApplication{}}
	for _, entry := range feedback.Entries {
		app := FeedbackApplication{FeedbackNodeID: entry.NodeID, ContentVersion: entry.ContentVersion, Access: IntegrationAccessNotEvaluated, State: FeedbackAppUnknown}
		if entry.Kind == PRConversationComment {
			app.Access = IntegrationAccessAvailable
			app.State = FeedbackAppAttributed
			app.Application = &CheckApplication{ID: "9007199254740993", NodeID: "APP_1", Slug: "reviewer-app"}
		}
		value.Applications = append(value.Applications, app)
	}
	return item, value
}
func TestReviewerORSelectorsNeedOriginalIdentityAndSpecificAppAttribution(t *testing.T) {
	item, v := reviewerDomainFixture()
	if v.Validate(item) != nil {
		t.Fatal("invalid fixture")
	}
	bot := ReviewerSelector{Kind: ReviewerBot, ID: "199175422", NodeID: "BOT_1"}
	app := ReviewerSelector{Kind: ReviewerApp, ID: "9007199254740993", NodeID: "APP_1"}
	permission := ReviewerSelector{Kind: ReviewerMinimumPermission, Permission: PermissionRead}
	if v.Match("REVIEW_1", nil) != ReviewerDoesNotMatch || v.Match("REVIEW_1", []ReviewerSelector{permission}) != ReviewerDoesNotMatch {
		t.Fatal("no selectors/public visibility granted automatic eligibility")
	}
	if v.Match("REVIEW_1", []ReviewerSelector{bot}) != ReviewerMatches || v.Match("REVIEW_1", []ReviewerSelector{app}) != ReviewerUnknown || v.Match("CONVERSATION_1", []ReviewerSelector{app}) != ReviewerMatches {
		t.Fatal("App/bot provenance conflated")
	}
	v.Actors[0].Permission = ReviewerPermission{Access: IntegrationAccessDenied}
	if v.Match("REVIEW_1", []ReviewerSelector{permission}) != ReviewerUnknown || v.Match("REVIEW_1", []ReviewerSelector{permission, bot}) != ReviewerMatches {
		t.Fatal("OR selectors coupled to unrelated permission failure")
	}
	v.Actors[0].IdentityAccess = IntegrationAccessUnavailable
	v.Actors[0].Identity = nil
	v.Actors[0].Permission = ReviewerPermission{Access: IntegrationAccessNotEvaluated}
	if v.Match("CONVERSATION_1", []ReviewerSelector{app}) != ReviewerUnknown {
		t.Fatal("unknown author authorized by historical App attribution")
	}
}
func TestReviewerPermissionIntervalsPreserveCustomRoleUncertainty(t *testing.T) {
	for _, row := range []struct {
		base, role       string
		minimum, maximum RepositoryPermission
		valid            bool
	}{
		{"read", "triage", PermissionTriage, PermissionTriage, true}, {"write", "maintain", PermissionMaintain, PermissionMaintain, true}, {"admin", "admin", PermissionAdmin, PermissionAdmin, true}, {"none", "", PermissionNone, PermissionNone, true},
		{"read", "custom-triage", PermissionRead, PermissionTriage, true}, {"write", "custom-maintainer", PermissionWrite, PermissionMaintain, true}, {"read", "admin", PermissionAdmin, PermissionAdmin, false}, {"none", "custom", "", "", false}, {"future", "custom", "", "", false},
	} {
		minimum, maximum, valid := PermissionInterval(row.base, row.role)
		if valid != row.valid || valid && (minimum != row.minimum || maximum != row.maximum) {
			t.Fatal("permission interval changed", row)
		}
	}
	_, v := reviewerDomainFixture()
	v.Actors[0].Permission = ReviewerPermission{Access: IntegrationAccessAvailable, LegacyPermission: "write", RoleName: "custom-maintainer", Minimum: PermissionWrite, Maximum: PermissionMaintain}
	for _, row := range []struct {
		threshold RepositoryPermission
		want      ReviewerMatch
	}{{PermissionRead, ReviewerMatches}, {PermissionWrite, ReviewerMatches}, {PermissionMaintain, ReviewerUnknown}, {PermissionAdmin, ReviewerDoesNotMatch}} {
		if v.Match("REVIEW_1", []ReviewerSelector{{Kind: ReviewerMinimumPermission, Permission: row.threshold}}) != row.want {
			t.Fatal("custom role threshold inferred", row)
		}
	}
}
func TestReviewerEvidenceRejectsForeignActorAppAndInflatedRole(t *testing.T) {
	for _, mode := range []string{"identity", "missing-actor", "duplicate", "permission", "app-version", "app-cross-comment", "bot-inferred-app", "unknown-identity"} {
		t.Run(mode, func(t *testing.T) {
			item, v := reviewerDomainFixture()
			switch mode {
			case "identity":
				v.Actors[0].Identity.ID = "17"
			case "missing-actor":
				v.Actors = []PRReviewerIdentity{}
			case "duplicate":
				v.Applications[1] = v.Applications[0]
			case "permission":
				v.Actors[0].Permission.Minimum = PermissionAdmin
			case "app-version":
				v.Applications[2].ContentVersion = "wrong"
			case "app-cross-comment":
				v.Applications[2].FeedbackNodeID = "another"
			case "bot-inferred-app":
				v.Applications[0].Access = IntegrationAccessAvailable
				v.Applications[0].State = FeedbackAppAttributed
				v.Applications[0].Application = v.Applications[2].Application
			case "unknown-identity":
				v.Actors[0].IdentityAccess = IntegrationAccessDenied
			}
			if v.Validate(item) == nil {
				t.Fatal("forged reviewer evidence accepted")
			}
		})
	}
	for _, selectors := range [][]ReviewerSelector{{{Kind: ReviewerUser, ID: "17"}}, {{Kind: ReviewerMinimumPermission, Permission: PermissionNone}}, {{Kind: ReviewerApp, ID: "17", NodeID: "A"}, {Kind: ReviewerApp, ID: "17", NodeID: "B"}}, {{Kind: ReviewerBot, ID: "17", NodeID: "B", Permission: PermissionAdmin}}} {
		if ValidateReviewerSelectors(selectors) == nil {
			t.Fatal("invalid stable selector accepted")
		}
	}
}
