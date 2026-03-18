export const AUTO_REPOSITORY_GROUP_PREFIX = "auto-repo-singleton-";

const GROUP_TARGET_PREFIX = "group:";
const REPOSITORY_TARGET_PREFIX = "repository:";

export type RepositoryTargetSelection =
  | { kind: "group"; repositoryGroupId: string }
  | { kind: "repository"; repositoryId: string };

export function isAutoRepositoryGroupId(repositoryGroupId: string | undefined): boolean {
  if (!repositoryGroupId) {
    return false;
  }
  return repositoryGroupId.startsWith(AUTO_REPOSITORY_GROUP_PREFIX);
}

export function repositoryIdFromAutoRepositoryGroupId(repositoryGroupId: string | undefined): string | null {
  if (!isAutoRepositoryGroupId(repositoryGroupId)) {
    return null;
  }
  const repositoryId = repositoryGroupId.slice(AUTO_REPOSITORY_GROUP_PREFIX.length);
  return repositoryId.length > 0 ? repositoryId : null;
}

export function encodeRepositoryTargetSelection(selection: RepositoryTargetSelection): string {
  if (selection.kind === "group") {
    return `${GROUP_TARGET_PREFIX}${selection.repositoryGroupId}`;
  }
  return `${REPOSITORY_TARGET_PREFIX}${selection.repositoryId}`;
}

export function decodeRepositoryTargetSelection(value: string): RepositoryTargetSelection | null {
  if (value.startsWith(GROUP_TARGET_PREFIX)) {
    const repositoryGroupId = value.slice(GROUP_TARGET_PREFIX.length).trim();
    if (repositoryGroupId.length === 0) {
      return null;
    }
    return { kind: "group", repositoryGroupId };
  }
  if (value.startsWith(REPOSITORY_TARGET_PREFIX)) {
    const repositoryId = value.slice(REPOSITORY_TARGET_PREFIX.length).trim();
    if (repositoryId.length === 0) {
      return null;
    }
    return { kind: "repository", repositoryId };
  }
  return null;
}

export function formatTaskRepositoryScope(repositoryGroupId: string | undefined): string {
  if (!repositoryGroupId) {
    return "No repository group";
  }
  const repositoryId = repositoryIdFromAutoRepositoryGroupId(repositoryGroupId);
  if (repositoryId) {
    return `Repository: ${repositoryId}`;
  }
  return `Group: ${repositoryGroupId}`;
}

export function formatTaskRepositoryScopeDetail(repositoryGroupId: string | undefined): string {
  if (!repositoryGroupId) {
    return "-";
  }
  const repositoryId = repositoryIdFromAutoRepositoryGroupId(repositoryGroupId);
  if (repositoryId) {
    return `Repository ${repositoryId}`;
  }
  return `Group ${repositoryGroupId}`;
}

