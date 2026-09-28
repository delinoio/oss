package github

// Separate fixed documents keep each connection's cursor independent. A nested
// thread with more than 100 comments is continued by its original node identity,
// while each response still binds the same repository and current PR operands.
const feedbackPRFields = `fragment DeliDevFeedbackPR on PullRequest {
 id number state merged baseRefName baseRefOid headRefName headRefOid repository { id }
}`
const feedbackAuthorFields = `fragment DeliDevFeedbackAuthor on Actor {
 __typename login ... on Node { id }
 ... on User { databaseId } ... on Bot { databaseId } ... on Organization { databaseId }
}`
const feedbackReviewFields = `fragment DeliDevFeedbackReview on PullRequestReview {
 id fullDatabaseId body state publishedAt lastEditedAt submittedAt url
 author { ...DeliDevFeedbackAuthor } pullRequest { id } repository { id }
}`
const feedbackCommentFields = `fragment DeliDevFeedbackComment on PullRequestReviewComment {
 id fullDatabaseId body state publishedAt lastEditedAt url path diffHunk line originalLine
 author { ...DeliDevFeedbackAuthor } pullRequest { id } repository { id }
 pullRequestReview { id state submittedAt }
}`
const feedbackThreadFields = `fragment DeliDevFeedbackThread on PullRequestReviewThread {
 id isResolved isOutdated pullRequest { ...DeliDevFeedbackPR } repository { id }
}`
const feedbackReviewsGraphQL = `query DeliDevPRReviews($id: ID!, $after: String) {
 node(id:$id) { ... on PullRequest { ...DeliDevFeedbackPR
 reviews(first:100,after:$after) { totalCount pageInfo { hasNextPage endCursor } nodes { ...DeliDevFeedbackReview } }
 } }
}` + feedbackPRFields + feedbackReviewFields + feedbackAuthorFields
const feedbackConversationGraphQL = `query DeliDevPRConversation($id: ID!, $after: String) {
 node(id:$id) { ... on PullRequest { ...DeliDevFeedbackPR
 comments(first:100,after:$after) { totalCount pageInfo { hasNextPage endCursor }
 nodes { id fullDatabaseId body publishedAt lastEditedAt url author { ...DeliDevFeedbackAuthor } pullRequest { id } repository { id } } }
 } }
}` + feedbackPRFields + feedbackAuthorFields
const feedbackThreadsGraphQL = `query DeliDevPRThreads($id: ID!, $after: String) {
 node(id:$id) { ... on PullRequest { ...DeliDevFeedbackPR
 reviewThreads(first:100,after:$after) { totalCount pageInfo { hasNextPage endCursor } nodes {
 ...DeliDevFeedbackThread
 comments(first:100) { totalCount pageInfo { hasNextPage endCursor } nodes { ...DeliDevFeedbackComment } }
 } }
 } }
}` + feedbackPRFields + feedbackThreadFields + feedbackCommentFields + feedbackAuthorFields
const feedbackThreadCommentsGraphQL = `query DeliDevPRThreadComments($id: ID!, $after: String) {
 node(id:$id) { ... on PullRequestReviewThread { ...DeliDevFeedbackThread
 comments(first:100,after:$after) { totalCount pageInfo { hasNextPage endCursor } nodes { ...DeliDevFeedbackComment } }
 } }
}` + feedbackPRFields + feedbackThreadFields + feedbackCommentFields + feedbackAuthorFields
