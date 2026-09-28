package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

const MaxPRFeedback = 500
const MaxPRFeedbackBytes = 512 << 10

type PRFeedbackKind string

const (
	PRReviewBody          PRFeedbackKind = "review"
	PRReviewComment       PRFeedbackKind = "review-comment"
	PRConversationComment PRFeedbackKind = "conversation-comment"
)

// Unknown GraphQL actor types remain visible without manufacturing a numeric
// user identity or treating a login suffix as proof of a bot or application.
type FeedbackAuthor struct {
	NativeType string              `json:"native_type"`
	Kind       RepositoryActorKind `json:"kind"`
	ID         string              `json:"id,omitempty"`
	NodeID     string              `json:"node_id"`
	Login      string              `json:"login"`
}

func (a FeedbackAuthor) Validate() error {
	if Text(a.NativeType, "author type", 64, true) != nil || Text(a.NodeID, "author node", 256, true) != nil || Text(a.Login, "author login", 100, true) != nil || strings.ContainsAny(a.NativeType+a.Login, "\r\n") {
		return invalidPRObservation()
	}
	switch a.NativeType {
	case "User", "Bot", "Organization":
		return (RepositoryActor{ProviderType: a.NativeType, Kind: a.Kind, ID: a.ID, NodeID: a.NodeID, Login: a.Login}).Validate()
	default:
		if a.Kind != RepositoryUnknownActor || a.ID != "" {
			return invalidPRObservation()
		}
	}
	return nil
}

type FeedbackCode struct {
	Path         string  `json:"path"`
	DiffHunk     string  `json:"diff_hunk"`
	Line         *uint32 `json:"line,omitempty"`
	OriginalLine *uint32 `json:"original_line,omitempty"`
}

type PRFeedback struct {
	Kind              PRFeedbackKind  `json:"kind"`
	ID                string          `json:"id"`
	NodeID            string          `json:"node_id"`
	Author            *FeedbackAuthor `json:"author,omitempty"`
	Body              string          `json:"body"`
	PublishedAt       time.Time       `json:"published_at"`
	LastEditedAt      *time.Time      `json:"last_edited_at,omitempty"`
	NativeState       string          `json:"native_state,omitempty"`
	ReviewState       string          `json:"review_state,omitempty"`
	ReviewSubmittedAt *time.Time      `json:"review_submitted_at,omitempty"`
	URL               string          `json:"url"`
	ContentVersion    string          `json:"content_version"`
	ReviewNodeID      string          `json:"review_node_id,omitempty"`
	ThreadNodeID      string          `json:"thread_node_id,omitempty"`
	Code              *FeedbackCode   `json:"code,omitempty"`
}

// Provider approval/dismissal, thread resolution, relocation and author display
// changes do not create a content edit. A later edit returning to the same body
// still has a new version through GitHub's original lastEditedAt timestamp.
func (v PRFeedback) Version() string {
	raw, _ := json.Marshal(struct {
		Kind         PRFeedbackKind `json:"kind"`
		NodeID       string         `json:"node_id"`
		Body         string         `json:"body"`
		PublishedAt  time.Time      `json:"published_at"`
		LastEditedAt *time.Time     `json:"last_edited_at"`
	}{v.Kind, v.NodeID, v.Body, v.PublishedAt, v.LastEditedAt})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func PublishedReviewState(state string) bool {
	return state == "APPROVED" || state == "CHANGES_REQUESTED" || state == "COMMENTED" || state == "DISMISSED"
}

func (v PRFeedback) Validate(item RepositoryItem) error {
	if !PositiveDecimal(v.ID) || Text(v.NodeID, "feedback node", 256, true) != nil || Text(v.Body, "feedback body", 64<<10, false) != nil || v.PublishedAt.IsZero() || v.LastEditedAt != nil && v.LastEditedAt.IsZero() || v.Author != nil && v.Author.Validate() != nil || v.ContentVersion != v.Version() {
		return invalidPRObservation()
	}
	fragment := ""
	switch v.Kind {
	case PRReviewBody:
		fragment = "pullrequestreview-"
		if v.ReviewSubmittedAt == nil || v.ReviewSubmittedAt.IsZero() || v.ReviewState != "" || !PublishedReviewState(v.NativeState) || v.ReviewNodeID != "" || v.ThreadNodeID != "" || v.Code != nil {
			return invalidPRObservation()
		}
	case PRConversationComment:
		fragment = "issuecomment-"
		if v.ReviewState != "" || v.ReviewSubmittedAt != nil || v.NativeState != "" || v.ReviewNodeID != "" || v.ThreadNodeID != "" || v.Code != nil {
			return invalidPRObservation()
		}
	case PRReviewComment:
		fragment = "discussion_r"
		if !PublishedReviewState(v.ReviewState) || v.ReviewSubmittedAt == nil || v.ReviewSubmittedAt.IsZero() || v.NativeState != "SUBMITTED" || Text(v.ReviewNodeID, "review node", 256, true) != nil || Text(v.ThreadNodeID, "thread node", 256, true) != nil || v.Code == nil {
			return invalidPRObservation()
		}
		if Text(v.Code.Path, "code path", 4096, true) != nil || Text(v.Code.DiffHunk, "code context", 64<<10, false) != nil || v.Code.Line != nil && (*v.Code.Line == 0 || *v.Code.Line > 2147483647) || v.Code.OriginalLine != nil && (*v.Code.OriginalLine == 0 || *v.Code.OriginalLine > 2147483647) {
			return invalidPRObservation()
		}
	default:
		return invalidPRObservation()
	}
	if v.URL != item.URL+"#"+fragment+v.ID {
		return invalidPRObservation()
	}
	return nil
}

type PRFeedbackThread struct {
	NodeID         string   `json:"node_id"`
	Resolved       bool     `json:"resolved"`
	Outdated       bool     `json:"outdated"`
	CommentNodes   []string `json:"comment_nodes"`
	ExcludedDrafts uint32   `json:"excluded_drafts"`
}

type PullRequestFeedback struct {
	BaseSHA              string             `json:"base_sha"`
	HeadSHA              string             `json:"head_sha"`
	Entries              []PRFeedback       `json:"entries"`
	Threads              []PRFeedbackThread `json:"threads"`
	ExcludedDraftReviews uint32             `json:"excluded_draft_reviews"`
}

func (v PullRequestFeedback) Validate(item RepositoryItem) error {
	if v.BaseSHA != item.BaseSHA || v.HeadSHA != item.HeadSHA || v.Entries == nil || v.Threads == nil || len(v.Threads) > MaxPRFeedback || uint64(len(v.Entries))+uint64(v.ExcludedDraftReviews) > MaxPRFeedback {
		return invalidPRObservation()
	}
	seen, numeric, reviews := map[string]PRFeedback{}, map[string]bool{}, map[string]bool{}
	bytes := 0
	for _, entry := range v.Entries {
		_, duplicate := seen[entry.NodeID]
		key := string(entry.Kind) + ":" + entry.ID
		if entry.Validate(item) != nil || duplicate || numeric[key] {
			return invalidPRObservation()
		}
		seen[entry.NodeID], numeric[key] = entry, true
		if entry.Kind == PRReviewBody {
			reviews[entry.NodeID] = true
		}
		bytes += len(entry.Body)
		if entry.Code != nil {
			bytes += len(entry.Code.DiffHunk) + len(entry.Code.Path)
		}
	}
	if bytes > MaxPRFeedbackBytes {
		return invalidPRObservation()
	}
	threads, comments := map[string]bool{}, map[string]bool{}
	total := uint64(len(v.Entries)) + uint64(v.ExcludedDraftReviews)
	for _, thread := range v.Threads {
		if Text(thread.NodeID, "thread node", 256, true) != nil || threads[thread.NodeID] || thread.CommentNodes == nil {
			return invalidPRObservation()
		}
		if _, exists := seen[thread.NodeID]; exists {
			return invalidPRObservation()
		}
		threads[thread.NodeID] = true
		total += uint64(thread.ExcludedDrafts)
		for _, id := range thread.CommentNodes {
			entry, exists := seen[id]
			if !exists || comments[id] || entry.Kind != PRReviewComment || entry.ThreadNodeID != thread.NodeID || !reviews[entry.ReviewNodeID] || seen[entry.ReviewNodeID].NativeState != entry.ReviewState || !seen[entry.ReviewNodeID].ReviewSubmittedAt.Equal(*entry.ReviewSubmittedAt) {
				return invalidPRObservation()
			}
			comments[id] = true
		}
	}
	if total > MaxPRFeedback {
		return invalidPRObservation()
	}
	for _, entry := range v.Entries {
		if entry.Kind == PRReviewComment && !comments[entry.NodeID] {
			return invalidPRObservation()
		}
	}
	return nil
}
