package service

import (
	"fmt"
	"strings"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database"
)

func notificationWrites(
	previous []*deckv1.PullRequestResult,
	current []*deckv1.PullRequestResult,
	preferences []*deckv1.ViewNotificationPreference,
	viewerLogin string,
) []database.NotificationEventWrite {
	enabled := make(map[deckv1.NotificationTransition]struct{})
	for _, preference := range preferences {
		if !preference.GetEnabled() {
			continue
		}
		for _, transition := range preference.GetTransitions() {
			enabled[transition] = struct{}{}
		}
	}
	if len(enabled) == 0 {
		return nil
	}
	before := make(map[string]*deckv1.PullRequestResult, len(previous))
	for _, snapshot := range previous {
		before[pullRequestSnapshotKey(snapshot)] = snapshot
	}
	var writes []database.NotificationEventWrite
	for _, snapshot := range current {
		old := before[pullRequestSnapshotKey(snapshot)]
		if old == nil {
			continue
		}
		for _, transition := range notificationTransitions(
			old, snapshot, viewerLogin) {
			if _, ok := enabled[transition]; !ok {
				continue
			}
			writes = append(writes, database.NotificationEventWrite{
				Transition: transition, Snapshot: snapshot,
			})
		}
	}
	return writes
}

func notificationTransitions(
	previous *deckv1.PullRequestResult,
	current *deckv1.PullRequestResult,
	viewerLogin string,
) []deckv1.NotificationTransition {
	if previous == nil || current == nil {
		return nil
	}
	var transitions []deckv1.NotificationTransition
	if !containsUser(previous.GetAssignees(), viewerLogin) &&
		containsUser(current.GetAssignees(), viewerLogin) {
		transitions = append(transitions,
			deckv1.NotificationTransition_NOTIFICATION_TRANSITION_ASSIGNED)
	}
	if !containsReviewer(previous.GetReviewers(), viewerLogin) &&
		containsReviewer(current.GetReviewers(), viewerLogin) {
		transitions = append(transitions,
			deckv1.NotificationTransition_NOTIFICATION_TRANSITION_REVIEW_REQUESTED)
	}
	if previous.GetChecks().GetState() !=
		deckv1.ChecksState_CHECKS_STATE_FAILURE &&
		current.GetChecks().GetState() ==
			deckv1.ChecksState_CHECKS_STATE_FAILURE {
		transitions = append(transitions,
			deckv1.NotificationTransition_NOTIFICATION_TRANSITION_CHECKS_FAILED)
	}
	if previous.GetMergeability() !=
		deckv1.Mergeability_MERGEABILITY_MERGEABLE &&
		current.GetMergeability() ==
			deckv1.Mergeability_MERGEABILITY_MERGEABLE {
		transitions = append(transitions,
			deckv1.NotificationTransition_NOTIFICATION_TRANSITION_BECAME_MERGEABLE)
	}
	if previous.GetMergeability() !=
		deckv1.Mergeability_MERGEABILITY_CONFLICTING &&
		current.GetMergeability() ==
			deckv1.Mergeability_MERGEABILITY_CONFLICTING {
		transitions = append(transitions,
			deckv1.NotificationTransition_NOTIFICATION_TRANSITION_CONFLICTED)
	}
	if previous.GetLifecycleState() !=
		deckv1.PullRequestLifecycleState_PULL_REQUEST_LIFECYCLE_STATE_MERGED &&
		current.GetLifecycleState() ==
			deckv1.PullRequestLifecycleState_PULL_REQUEST_LIFECYCLE_STATE_MERGED {
		transitions = append(transitions,
			deckv1.NotificationTransition_NOTIFICATION_TRANSITION_MERGED)
	}
	if previous.GetLifecycleState() ==
		deckv1.PullRequestLifecycleState_PULL_REQUEST_LIFECYCLE_STATE_OPEN &&
		current.GetLifecycleState() ==
			deckv1.PullRequestLifecycleState_PULL_REQUEST_LIFECYCLE_STATE_CLOSED {
		transitions = append(transitions,
			deckv1.NotificationTransition_NOTIFICATION_TRANSITION_CLOSED)
	}
	return transitions
}

func pullRequestSnapshotKey(snapshot *deckv1.PullRequestResult) string {
	repository := snapshot.GetRepository()
	return fmt.Sprintf("%s/%s#%d",
		strings.ToLower(repository.GetOwner()),
		strings.ToLower(repository.GetName()),
		snapshot.GetNumber())
}

func containsUser(users []*deckv1.GitHubUser, login string) bool {
	for _, user := range users {
		if strings.EqualFold(user.GetLogin(), login) {
			return true
		}
	}
	return false
}

func containsReviewer(
	reviewers []*deckv1.PullRequestReviewer,
	login string,
) bool {
	for _, reviewer := range reviewers {
		if strings.EqualFold(reviewer.GetUser().GetLogin(), login) {
			return true
		}
	}
	return false
}
