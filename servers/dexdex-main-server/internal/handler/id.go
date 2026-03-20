package handler

import "github.com/google/uuid"

func nextSubTaskID() string {
	return "sub-" + uuid.NewString()
}

func nextSessionID() string {
	return "session-" + uuid.NewString()
}

func nextReviewCommentID() string {
	return "rc-" + uuid.NewString()
}
