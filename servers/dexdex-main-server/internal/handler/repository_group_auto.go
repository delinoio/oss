package handler

import "strings"

const autoRepositoryGroupPrefix = "auto-repo-singleton-"

func buildAutoRepositoryGroupID(repositoryID string) string {
	return autoRepositoryGroupPrefix + repositoryID
}

func isAutoRepositoryGroupID(repositoryGroupID string) bool {
	return strings.HasPrefix(repositoryGroupID, autoRepositoryGroupPrefix)
}

func repositoryIDFromAutoRepositoryGroupID(repositoryGroupID string) (string, bool) {
	if !isAutoRepositoryGroupID(repositoryGroupID) {
		return "", false
	}
	repositoryID := strings.TrimPrefix(repositoryGroupID, autoRepositoryGroupPrefix)
	if repositoryID == "" {
		return "", false
	}
	return repositoryID, true
}
