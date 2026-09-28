package github

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func sameFeedbackActor(original *domain.FeedbackAuthor, current *domain.RepositoryActor) bool {
	if original == nil || current == nil {
		return original == nil && current == nil
	}
	return original.ID == current.ID && original.NodeID == current.NodeID && original.Kind == current.Kind && original.NativeType == current.ProviderType
}

func (c *Client) readReviewerIdentity(ctx context.Context, token []byte, repository domain.RemoteRepository, author domain.FeedbackAuthor) domain.PRReviewerIdentity {
	v := domain.PRReviewerIdentity{Author: author, IdentityAccess: domain.IntegrationAccessNotEvaluated, Permission: domain.ReviewerPermission{Access: domain.IntegrationAccessNotEvaluated}}
	if author.Kind != domain.RepositoryUser && author.Kind != domain.RepositoryBot {
		return v
	}
	read := c.readRepositoryJSON(ctx, token, "/user/"+author.ID)
	v.IdentityAccess = read.state
	if read.state != domain.IntegrationAccessAvailable {
		return v
	}
	actor, ok := parseActor(read.raw)
	if !ok || !sameFeedbackActor(&author, actor) {
		v.IdentityAccess = domain.IntegrationAccessUnavailable
		return v
	}
	v.Identity = actor
	// The current REST login comes from an independently matched durable ID.
	// In particular, never construct a Bot login from a display suffix.
	read = c.readRepositoryJSON(ctx, token, repositoryPath(repository)+"/collaborators/"+url.PathEscape(actor.Login)+"/permission")
	v.Permission.Access = read.state
	if read.state != domain.IntegrationAccessAvailable {
		return v
	}
	f, ok := jsonObject(read.raw)
	permissionActor, actorOK := parseActor(f["user"])
	legacy, role := stringField(f, "permission"), stringField(f, "role_name")
	minimum, maximum, valid := domain.PermissionInterval(legacy, role)
	if actorOK && permissionActor != nil && *permissionActor != *actor {
		// A later contradictory identity cannot leave an earlier exact-author match usable.
		v.IdentityAccess = domain.IntegrationAccessUnavailable
		v.Identity = nil
		v.Permission = domain.ReviewerPermission{Access: domain.IntegrationAccessNotEvaluated}
		return v
	}
	if !ok || !actorOK || permissionActor == nil || !valid {
		v.Permission.Access = domain.IntegrationAccessUnavailable
		return v
	}
	if raw, exists := f["role_name"]; !exists || string(raw) == "null" || json.Unmarshal(raw, &role) != nil {
		v.Permission.Access = domain.IntegrationAccessUnavailable
		return v
	}
	v.Permission = domain.ReviewerPermission{Access: domain.IntegrationAccessAvailable, LegacyPermission: legacy, RoleName: role, Minimum: minimum, Maximum: maximum}
	return v
}

func parseFeedbackApplication(raw []byte, entry domain.PRFeedback, repository domain.RemoteRepository, item domain.RepositoryItem) (domain.FeedbackApplication, error) {
	v := domain.FeedbackApplication{FeedbackNodeID: entry.NodeID, ContentVersion: entry.ContentVersion, Access: domain.IntegrationAccessAvailable, State: domain.FeedbackAppUnknown}
	f, ok := jsonObject(raw)
	id, idOK := exactUnsigned(f["id"])
	author, authorOK := parseActor(f["user"])
	var body string
	created, createdErr := time.Parse(time.RFC3339Nano, stringField(f, "created_at"))
	if !ok || !idOK || strconv.FormatUint(id, 10) != entry.ID || stringField(f, "node_id") != entry.NodeID || !authorOK || !sameFeedbackActor(entry.Author, author) || json.Unmarshal(f["body"], &body) != nil || string(f["body"]) == "null" || body != entry.Body || createdErr != nil || !created.Equal(entry.PublishedAt) || stringField(f, "html_url") != entry.URL || stringField(f, "issue_url") != apiOrigin+repositoryPath(repository)+"/issues/"+item.Number {
		return v, queryUnavailable()
	}
	appRaw, exists := f["performed_via_github_app"]
	if !exists {
		return v, nil
	}
	if string(appRaw) == "null" {
		v.State = domain.FeedbackAppNone
		return v, nil
	}
	app, ok := jsonObject(appRaw)
	appID, idOK := exactUnsigned(app["id"])
	if !ok || !idOK {
		return v, queryUnavailable()
	}
	v.State = domain.FeedbackAppAttributed
	v.Application = &domain.CheckApplication{ID: strconv.FormatUint(appID, 10), NodeID: stringField(app, "node_id"), Slug: stringField(app, "slug")}
	if v.Validate(entry) != nil {
		return v, queryUnavailable()
	}
	return v, nil
}

func (c *Client) readFeedbackApplications(ctx context.Context, token []byte, repository domain.RemoteRepository, item domain.RepositoryItem, feedback domain.PullRequestFeedback) []domain.FeedbackApplication {
	result := make([]domain.FeedbackApplication, 0, len(feedback.Entries))
	comments := map[string]domain.PRFeedback{}
	for _, entry := range feedback.Entries {
		result = append(result, domain.FeedbackApplication{FeedbackNodeID: entry.NodeID, ContentVersion: entry.ContentVersion, Access: domain.IntegrationAccessNotEvaluated, State: domain.FeedbackAppUnknown})
		if entry.Kind == domain.PRConversationComment {
			comments[entry.NodeID] = entry
		}
	}
	if len(comments) == 0 {
		return result
	}
	unknown := func(state domain.IntegrationAccessState) []domain.FeedbackApplication {
		for i := range result {
			if _, exists := comments[result[i].FeedbackNodeID]; exists {
				result[i].Access = state
			}
		}
		return result
	}
	observed := map[string]domain.FeedbackApplication{}
	for page := uint32(1); page <= 5; page++ {
		path := repositoryPath(repository) + "/issues/" + item.Number + "/comments?" + url.Values{"page": {strconv.FormatUint(uint64(page), 10)}, "per_page": {"100"}}.Encode()
		read := c.readRepositoryJSON(ctx, token, path)
		if read.state != domain.IntegrationAccessAvailable {
			return unknown(read.state)
		}
		var rows []json.RawMessage
		if domain.Decode(read.raw, &rows) != nil || rows == nil || len(rows) > 100 {
			return unknown(domain.IntegrationAccessUnavailable)
		}
		for _, raw := range rows {
			f, ok := jsonObject(raw)
			id := stringField(f, "node_id")
			entry, exists := comments[id]
			if !ok || !exists {
				return unknown(domain.IntegrationAccessUnavailable)
			}
			if _, duplicate := observed[id]; duplicate {
				return unknown(domain.IntegrationAccessUnavailable)
			}
			value, err := parseFeedbackApplication(raw, entry, repository, item)
			if err != nil {
				return unknown(domain.IntegrationAccessUnavailable)
			}
			observed[id] = value
		}
		next, err := queryNextPage(read.link, path, page, repository)
		if err != nil || next != 0 && (page == 5 || len(rows) == 0) {
			return unknown(domain.IntegrationAccessUnavailable)
		}
		if next == 0 {
			break
		}
	}
	if len(observed) != len(comments) {
		return unknown(domain.IntegrationAccessUnavailable)
	}
	for i := range result {
		if value, exists := observed[result[i].FeedbackNodeID]; exists {
			result[i] = value
		}
	}
	return result
}

func (c *Client) readReviewerEvidence(ctx context.Context, token []byte, repository domain.RemoteRepository, item domain.RepositoryItem, feedback domain.PullRequestFeedback) (*domain.PullRequestReviewers, error) {
	result := &domain.PullRequestReviewers{Feedback: feedback, Actors: []domain.PRReviewerIdentity{}}
	authors := map[string]domain.FeedbackAuthor{}
	for _, entry := range feedback.Entries {
		if entry.Author == nil {
			continue
		}
		if previous, exists := authors[entry.Author.NodeID]; exists && previous != *entry.Author {
			return nil, queryUnavailable()
		}
		authors[entry.Author.NodeID] = *entry.Author
	}
	for _, author := range authors {
		result.Actors = append(result.Actors, domain.PRReviewerIdentity{Author: author})
	}
	sort.Slice(result.Actors, func(i, j int) bool { return result.Actors[i].Author.NodeID < result.Actors[j].Author.NodeID })
	// Each worker owns one result index at a time. All readers join before the
	// selected profile can clear its credential buffer or publish an observation.
	jobs := make(chan int, len(result.Actors))
	for i := range result.Actors {
		jobs <- i
	}
	close(jobs)
	var joined sync.WaitGroup
	for range 2 {
		joined.Add(1)
		go func() {
			defer joined.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				result.Actors[i] = c.readReviewerIdentity(ctx, token, repository, result.Actors[i].Author)
			}
		}()
	}
	joined.Wait()
	if ctx.Err() != nil {
		return nil, domain.SafeError(ctx.Err())
	}
	result.Applications = c.readFeedbackApplications(ctx, token, repository, item, feedback)
	if ctx.Err() != nil {
		return nil, domain.SafeError(ctx.Err())
	}
	if result.Validate(item) != nil {
		return nil, queryUnavailable()
	}
	return result, nil
}
