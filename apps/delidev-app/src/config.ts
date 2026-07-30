const CANONICAL_ORIGIN = "https://deli.dev";
const CANONICAL_AUDIENCE = "https://delibase.deli.dev";
const CANONICAL_DECK_AUDIENCE = "https://deck.deli.dev";
const CANONICAL_DECK_GITHUB_CALLBACK_URI =
  "https://deck.deli.dev/github/oauth/callback";

interface PublicEnvironment {
  readonly PUBLIC_DECK_API_ORIGIN?: string;
  readonly PUBLIC_DECK_GITHUB_APP_CLIENT_ID?: string;
  readonly PUBLIC_DECK_GITHUB_APP_SLUG?: string;
  readonly PUBLIC_DECK_GITHUB_CALLBACK_URI?: string;
  readonly PUBLIC_DECK_LOGTO_AUDIENCE?: string;
  readonly PUBLIC_DELIBASE_API_ORIGIN?: string;
  readonly PUBLIC_LOGTO_ENDPOINT?: string;
  readonly PUBLIC_LOGTO_APP_ID?: string;
  readonly PUBLIC_LOGTO_AUDIENCE?: string;
}

const publicEnvironment: PublicEnvironment = {
  PUBLIC_DECK_API_ORIGIN: import.meta.env.PUBLIC_DECK_API_ORIGIN,
  PUBLIC_DECK_GITHUB_APP_CLIENT_ID:
    import.meta.env.PUBLIC_DECK_GITHUB_APP_CLIENT_ID,
  PUBLIC_DECK_GITHUB_APP_SLUG:
    import.meta.env.PUBLIC_DECK_GITHUB_APP_SLUG,
  PUBLIC_DECK_GITHUB_CALLBACK_URI:
    import.meta.env.PUBLIC_DECK_GITHUB_CALLBACK_URI,
  PUBLIC_DECK_LOGTO_AUDIENCE:
    import.meta.env.PUBLIC_DECK_LOGTO_AUDIENCE,
  PUBLIC_DELIBASE_API_ORIGIN:
    import.meta.env.PUBLIC_DELIBASE_API_ORIGIN,
  PUBLIC_LOGTO_ENDPOINT: import.meta.env.PUBLIC_LOGTO_ENDPOINT,
  PUBLIC_LOGTO_APP_ID: import.meta.env.PUBLIC_LOGTO_APP_ID,
  PUBLIC_LOGTO_AUDIENCE: import.meta.env.PUBLIC_LOGTO_AUDIENCE,
};

export interface RuntimeConfig {
  apiOrigin: string;
  appOrigin: string;
  deck: {
    apiOrigin: string;
    audience: string;
    githubAppClientId: string;
    githubAppSlug: string;
    githubCallbackUri: string;
    issues: string[];
  };
  logto: {
    endpoint: string;
    appId: string;
    audience: string;
  };
  issues: string[];
}

function validHttpsUrl(value: string): boolean {
  try {
    return new URL(value).protocol === "https:";
  } catch {
    return false;
  }
}

function validGitHubClientId(value: string): boolean {
  return /^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$/.test(value);
}

function validGitHubAppSlug(value: string): boolean {
  return /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(
    value,
  );
}

export function readRuntimeConfig(
  environment: PublicEnvironment = publicEnvironment,
  browserOrigin =
    typeof window === "undefined" ? CANONICAL_ORIGIN : window.location.origin,
): RuntimeConfig {
  const apiOrigin = environment.PUBLIC_DELIBASE_API_ORIGIN?.trim() ?? "";
  const endpoint = environment.PUBLIC_LOGTO_ENDPOINT?.trim() ?? "";
  const appId = environment.PUBLIC_LOGTO_APP_ID?.trim() ?? "";
  const audience = environment.PUBLIC_LOGTO_AUDIENCE?.trim() ?? "";
  const deckApiOrigin =
    environment.PUBLIC_DECK_API_ORIGIN?.trim() ?? "";
  const deckAudience =
    environment.PUBLIC_DECK_LOGTO_AUDIENCE?.trim() ?? "";
  const githubAppClientId =
    environment.PUBLIC_DECK_GITHUB_APP_CLIENT_ID?.trim() ?? "";
  const githubAppSlug =
    environment.PUBLIC_DECK_GITHUB_APP_SLUG?.trim() ?? "";
  const githubCallbackUri =
    environment.PUBLIC_DECK_GITHUB_CALLBACK_URI?.trim() ?? "";
  const issues: string[] = [];
  const deckIssues: string[] = [];

  if (apiOrigin !== CANONICAL_AUDIENCE) {
    issues.push(
      `PUBLIC_DELIBASE_API_ORIGIN must be ${CANONICAL_AUDIENCE}.`,
    );
  }
  if (!validHttpsUrl(endpoint)) {
    issues.push("PUBLIC_LOGTO_ENDPOINT must be an HTTPS URL.");
  }
  if (!appId) {
    issues.push("PUBLIC_LOGTO_APP_ID is required.");
  }
  if (audience !== CANONICAL_AUDIENCE) {
    issues.push(
      `PUBLIC_LOGTO_AUDIENCE must be ${CANONICAL_AUDIENCE}.`,
    );
  }
  if (deckApiOrigin !== CANONICAL_DECK_AUDIENCE) {
    deckIssues.push(
      `PUBLIC_DECK_API_ORIGIN must be ${CANONICAL_DECK_AUDIENCE}.`,
    );
  }
  if (deckAudience !== CANONICAL_DECK_AUDIENCE) {
    deckIssues.push(
      `PUBLIC_DECK_LOGTO_AUDIENCE must be ${CANONICAL_DECK_AUDIENCE}.`,
    );
  }
  if (!validGitHubClientId(githubAppClientId)) {
    deckIssues.push(
      "PUBLIC_DECK_GITHUB_APP_CLIENT_ID is missing or invalid.",
    );
  }
  if (!validGitHubAppSlug(githubAppSlug)) {
    deckIssues.push(
      "PUBLIC_DECK_GITHUB_APP_SLUG is missing or invalid.",
    );
  }
  if (githubCallbackUri !== CANONICAL_DECK_GITHUB_CALLBACK_URI) {
    deckIssues.push(
      `PUBLIC_DECK_GITHUB_CALLBACK_URI must be ${CANONICAL_DECK_GITHUB_CALLBACK_URI}.`,
    );
  }

  return {
    apiOrigin,
    appOrigin: browserOrigin,
    deck: {
      apiOrigin: deckApiOrigin,
      audience: deckAudience,
      githubAppClientId,
      githubAppSlug,
      githubCallbackUri,
      issues: deckIssues,
    },
    logto: { endpoint, appId, audience },
    issues,
  };
}

export const runtimeConfig = readRuntimeConfig();
export const canonicalOrigin = CANONICAL_ORIGIN;
export const canonicalAudience = CANONICAL_AUDIENCE;
export const canonicalDeckAudience = CANONICAL_DECK_AUDIENCE;
export const canonicalDeckGitHubCallbackUri =
  CANONICAL_DECK_GITHUB_CALLBACK_URI;
