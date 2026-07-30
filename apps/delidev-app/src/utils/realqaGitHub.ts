import type { RealQAConfig } from "../config";

const githubOrigin = "https://github.com";
const oauthPath = "/login/oauth/authorize";

function hasExactlyOne(
  parameters: URLSearchParams,
  name: string,
): boolean {
  return parameters.getAll(name).length === 1;
}

function hasUnsafeRawPath(target: string): boolean {
  const match = /^https:\/\/github\.com(?::443)?([^?#]*)/i.exec(target);
  const rawPath = match?.[1] ?? "";
  if (/%2f|%5c/i.test(rawPath)) return true;
  return rawPath.split("/").some((segment) => {
    try {
      const decoded = decodeURIComponent(segment);
      return decoded === "." || decoded === ".." || decoded.includes("\\");
    } catch {
      return true;
    }
  });
}

export function isRealQAGitHubAuthorizationTarget(
  target: string,
  config: RealQAConfig,
): boolean {
  if (config.issues.length > 0 || hasUnsafeRawPath(target)) return false;
  let url: URL;
  try {
    url = new URL(target);
  } catch {
    return false;
  }
  if (
    url.origin !== githubOrigin ||
    url.protocol !== "https:" ||
    url.username ||
    url.password ||
    url.port ||
    url.hash
  ) {
    return false;
  }

  const keys = new Set(url.searchParams.keys());
  const stateIsValid =
    hasExactlyOne(url.searchParams, "state") &&
    (url.searchParams.get("state")?.length ?? 0) >= 32;
  if (!stateIsValid) return false;

  if (url.pathname === oauthPath) {
    if (
      !hasExactlyOne(url.searchParams, "client_id") ||
      url.searchParams.get("client_id") !== config.githubAppClientId
    ) {
      return false;
    }
    const redirectUris = url.searchParams.getAll("redirect_uri");
    if (
      redirectUris.length > 1 ||
      (redirectUris.length === 1 &&
        redirectUris[0] !== config.githubCallbackUri)
    ) {
      return false;
    }
    return [...keys].every((key) =>
      ["client_id", "redirect_uri", "state"].includes(key),
    );
  }

  const installPath =
    `/apps/${config.githubAppSlug}/installations/new`;
  return (
    url.pathname === installPath &&
    [...keys].every((key) => key === "state")
  );
}

export function navigateToRealQAGitHubAuthorization(
  target: string,
  config: RealQAConfig,
  navigate: (url: string) => void = (url) => window.location.assign(url),
): void {
  if (!isRealQAGitHubAuthorizationTarget(target, config)) {
    throw new Error(
      "RealQA rejected an invalid GitHub.com authorization target.",
    );
  }
  navigate(target);
}
