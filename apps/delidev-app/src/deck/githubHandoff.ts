export const deckReturnPathStorageKey = "delidev:deck:return-to";

export interface DeckGitHubAuthorizationConfig {
  githubAppClientId: string;
  githubAppSlug: string;
  githubCallbackUri: string;
}

type TopLevelNavigator = (target: string) => void;

function hasUnsafeRawPath(target: string): boolean {
  const rawPath = target.match(
    /^https:\/\/github\.com(?::443)?([^?#]*)/i,
  )?.[1];
  return (
    !rawPath ||
    /%2f|%5c|%2e|\\|(?:^|\/)\.{1,2}(?:\/|$)/i.test(rawPath)
  );
}

function hasExactQuery(
  url: URL,
  expected: Readonly<Record<string, string | undefined>>,
): boolean {
  const expectedKeys = Object.keys(expected);
  if ([...url.searchParams.keys()].some((key) => !expectedKeys.includes(key))) {
    return false;
  }
  return expectedKeys.every((key) => {
    const values = url.searchParams.getAll(key);
    const expectedValue = expected[key];
    return (
      values.length === 1 &&
      values[0] !== "" &&
      (key !== "state" ||
        /^[A-Za-z0-9._~-]{32,}$/.test(values[0] ?? "")) &&
      (expectedValue === undefined || values[0] === expectedValue)
    );
  });
}

export function isValidDeckGitHubAuthorizationTarget(
  target: string,
  configuration: DeckGitHubAuthorizationConfig,
): boolean {
  if (hasUnsafeRawPath(target)) return false;
  try {
    const url = new URL(target);
    if (
      url.protocol !== "https:" ||
      url.hostname !== "github.com" ||
      url.username ||
      url.password ||
      url.port ||
      url.hash
    ) {
      return false;
    }
    if (url.pathname === "/login/oauth/authorize") {
      return hasExactQuery(url, {
        client_id: configuration.githubAppClientId,
        redirect_uri: configuration.githubCallbackUri,
        state: undefined,
      });
    }
    if (
      url.pathname ===
      `/apps/${configuration.githubAppSlug}/installations/new`
    ) {
      return hasExactQuery(url, { state: undefined });
    }
    return false;
  } catch {
    return false;
  }
}

export function handoffDeckGitHubAuthorization(
  target: string,
  returnPath: string,
  configuration: DeckGitHubAuthorizationConfig,
  navigate: TopLevelNavigator = (url) => window.location.assign(url),
): void {
  if (
    !isValidDeckGitHubAuthorizationTarget(target, configuration) ||
    !(
      returnPath === "/account" ||
      /^\/o\/[a-z0-9]+(?:-[a-z0-9]+)*\/settings$/.test(returnPath)
    )
  ) {
    throw new Error(
      "Deck rejected an invalid GitHub.com authorization handoff.",
    );
  }
  sessionStorage.setItem(deckReturnPathStorageKey, returnPath);
  try {
    navigate(target);
  } catch (error) {
    sessionStorage.removeItem(deckReturnPathStorageKey);
    throw error;
  }
}
