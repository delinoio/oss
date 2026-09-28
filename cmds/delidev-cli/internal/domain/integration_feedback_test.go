package domain

import (
	"strings"
	"testing"
	"time"
)

func feedbackDomainFixture() (RepositoryItem, PullRequestFeedback) {
	item := RepositoryItem{URL: "https://github.com/fixture-owner/repo/pull/17", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
	when := time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)
	review := PRFeedback{Kind: PRReviewBody, ID: "9007199254740993", NodeID: "REVIEW_1", Body: "Approved, with follow-up feedback.", NativeState: "APPROVED", PublishedAt: when, ReviewSubmittedAt: &when, URL: item.URL + "#pullrequestreview-9007199254740993"}
	comment := PRFeedback{Kind: PRReviewComment, ID: "23", NodeID: "COMMENT_1", Body: "Please check this edge case.", NativeState: "SUBMITTED", ReviewState: "APPROVED", ReviewSubmittedAt: &when, PublishedAt: when, URL: item.URL + "#discussion_r23", ReviewNodeID: review.NodeID, ThreadNodeID: "THREAD_1", Code: &FeedbackCode{Path: "file.go", DiffHunk: "@@ -1 +1 @@\n-old\n+new"}}
	conversation := PRFeedback{Kind: PRConversationComment, ID: "27", NodeID: "CONVERSATION_1", Body: "Another observation", PublishedAt: when, URL: item.URL + "#issuecomment-27"}
	entries := []PRFeedback{review, comment, conversation}
	for i := range entries {
		entries[i].ContentVersion = entries[i].Version()
	}
	return item, PullRequestFeedback{BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA, Entries: entries, Threads: []PRFeedbackThread{{NodeID: "THREAD_1", CommentNodes: []string{comment.NodeID}}}}
}

func TestFeedbackVersionSeparatesEditsFromProviderReviewState(t *testing.T) {
	item, value := feedbackDomainFixture()
	if err := value.Validate(item); err != nil {
		t.Fatal(err)
	}
	version := value.Entries[0].ContentVersion
	value.Entries[0].NativeState = "DISMISSED"
	value.Entries[1].ReviewState = "DISMISSED"
	value.Threads[0].Resolved, value.Threads[0].Outdated = true, true
	if value.Entries[0].Version() != version || value.Validate(item) != nil {
		t.Fatal("review dismissal lost or changed published content")
	}
	edited := value.Entries[0].PublishedAt.Add(time.Minute)
	value.Entries[0].LastEditedAt = &edited
	if value.Entries[0].Version() == version {
		t.Fatal("a later edit returning to identical content lost its new version")
	}
	if value.Validate(item) == nil {
		t.Fatal("stale recorded version accepted")
	}
	value.Entries[0].ContentVersion = value.Entries[0].Version()
	if value.Validate(item) != nil {
		t.Fatal("edited content rejected")
	}
}

func TestFeedbackInventoryRejectsDraftsForeignReferencesAndForgedVersions(t *testing.T) {
	for _, mode := range []string{"draft", "missing-review", "different-parent-state", "foreign-url", "duplicate-node", "duplicate-number", "missing-thread", "foreign-thread", "capacity", "version", "bytes"} {
		t.Run(mode, func(t *testing.T) {
			item, value := feedbackDomainFixture()
			switch mode {
			case "draft":
				value.Entries[0].NativeState = "PENDING"
			case "missing-review":
				value.Entries = value.Entries[1:]
			case "different-parent-state":
				value.Entries[1].ReviewState = "DISMISSED"
			case "foreign-url":
				value.Entries[1].URL = "https://github.com/another/repo/pull/17#discussion_r23"
			case "duplicate-node":
				value.Entries = append(value.Entries, value.Entries[0])
			case "duplicate-number":
				entry := value.Entries[0]
				entry.NodeID = "another"
				entry.ContentVersion = entry.Version()
				value.Entries = append(value.Entries, entry)
			case "missing-thread":
				value.Threads = []PRFeedbackThread{}
			case "foreign-thread":
				value.Threads[0].NodeID = "OTHER_THREAD"
			case "capacity":
				value.ExcludedDraftReviews = MaxPRFeedback
			case "version":
				value.Entries[0].Body += "changed"
			case "bytes":
				value.Entries[0].Body = strings.Repeat("x", (64<<10)+1)
				value.Entries[0].ContentVersion = value.Entries[0].Version()
			}
			if value.Validate(item) == nil {
				t.Fatal("inconsistent feedback accepted")
			}
		})
	}
}

func TestFeedbackAuthorDoesNotInferBotOrAppFromLogin(t *testing.T) {
	unknown := FeedbackAuthor{NativeType: "FutureActor", Kind: RepositoryUnknownActor, NodeID: "UNKNOWN_1", Login: "appears-bot[bot]"}
	if unknown.Validate() != nil {
		t.Fatal("unknown author unavailable for manual inspection")
	}
	unknown.Kind = RepositoryBot
	if unknown.Validate() == nil {
		t.Fatal("display name upgraded actor identity")
	}
	bot := FeedbackAuthor{NativeType: "Bot", Kind: RepositoryBot, NodeID: "BOT_1", ID: "199175422", Login: "chatgpt-codex-connector"}
	if bot.Validate() != nil {
		t.Fatal("native GraphQL bot identity rejected")
	}
}
