const CANONICAL_ORIGIN = "https://deli.dev";
const CANONICAL_AUDIENCE = "https://delibase.deli.dev";
const CANONICAL_DECK_AUDIENCE = "https://deck.deli.dev";
const CANONICAL_DECK_GITHUB_CALLBACK_URI =
  "https://deck.deli.dev/github/oauth/callback";
const CANONICAL_REALQA_AUDIENCE = "https://realqa.deli.dev";
const CANONICAL_REALQA_CALLBACK =
  "https://realqa.deli.dev/github/oauth/callback";

export interface PublicEnvironment {
  readonly PUBLIC_DECK_API_ORIGIN?: string;
  readonly PUBLIC_DECK_GITHUB_APP_CLIENT_ID?: string;
  readonly PUBLIC_DECK_GITHUB_APP_SLUG?: string;
  readonly PUBLIC_DECK_GITHUB_CALLBACK_URI?: string;
  readonly PUBLIC_DECK_LOGTO_AUDIENCE?: string;
  readonly PUBLIC_DELIBASE_API_ORIGIN?: string;
  readonly PUBLIC_LOGTO_ENDPOINT?: string;
  readonly PUBLIC_LOGTO_APP_ID?: string;
  readonly PUBLIC_LOGTO_AUDIENCE?: string;
  readonly PUBLIC_REALQA_API_ORIGIN?: string;
  readonly PUBLIC_REALQA_LOGTO_AUDIENCE?: string;
  readonly PUBLIC_REALQA_GITHUB_APP_CLIENT_ID?: string;
  readonly PUBLIC_REALQA_GITHUB_APP_SLUG?: string;
  readonly PUBLIC_REALQA_GITHUB_CALLBACK_URI?: string;
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
  PUBLIC_REALQA_API_ORIGIN:
    import.meta.env.PUBLIC_REALQA_API_ORIGIN,
  PUBLIC_REALQA_LOGTO_AUDIENCE:
    import.meta.env.PUBLIC_REALQA_LOGTO_AUDIENCE,
  PUBLIC_REALQA_GITHUB_APP_CLIENT_ID:
    import.meta.env.PUBLIC_REALQA_GITHUB_APP_CLIENT_ID,
  PUBLIC_REALQA_GITHUB_APP_SLUG:
    import.meta.env.PUBLIC_REALQA_GITHUB_APP_SLUG,
  PUBLIC_REALQA_GITHUB_CALLBACK_URI:
    import.meta.env.PUBLIC_REALQA_GITHUB_CALLBACK_URI,
};

export interface RealQAConfig {
  apiOrigin: string;
  audience: string;
  githubAppClientId: string;
  githubAppSlug: string;
  githubCallbackUri: string;
  issues: string[];
}

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
  realqa: RealQAConfig;
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
  const deckGitHubAppClientId =
    environment.PUBLIC_DECK_GITHUB_APP_CLIENT_ID?.trim() ?? "";
  const deckGitHubAppSlug =
    environment.PUBLIC_DECK_GITHUB_APP_SLUG?.trim() ?? "";
  const deckGitHubCallbackUri =
    environment.PUBLIC_DECK_GITHUB_CALLBACK_URI?.trim() ?? "";
  const realqaApiOrigin =
    environment.PUBLIC_REALQA_API_ORIGIN?.trim() ?? "";
  const realqaAudience =
    environment.PUBLIC_REALQA_LOGTO_AUDIENCE?.trim() ?? "";
  const realqaGitHubAppClientId =
    environment.PUBLIC_REALQA_GITHUB_APP_CLIENT_ID?.trim() ?? "";
  const realqaGitHubAppSlug =
    environment.PUBLIC_REALQA_GITHUB_APP_SLUG?.trim() ?? "";
  const realqaGitHubCallbackUri =
    environment.PUBLIC_REALQA_GITHUB_CALLBACK_URI?.trim() ?? "";
  const issues: string[] = [];
  const deckIssues: string[] = [];
  const realqaIssues: string[] = [];

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
  if (!validGitHubClientId(deckGitHubAppClientId)) {
    deckIssues.push(
      "PUBLIC_DECK_GITHUB_APP_CLIENT_ID is missing or invalid.",
    );
  }
  if (!validGitHubAppSlug(deckGitHubAppSlug)) {
    deckIssues.push(
      "PUBLIC_DECK_GITHUB_APP_SLUG is missing or invalid.",
    );
  }
  if (deckGitHubCallbackUri !== CANONICAL_DECK_GITHUB_CALLBACK_URI) {
    deckIssues.push(
      `PUBLIC_DECK_GITHUB_CALLBACK_URI must be ${CANONICAL_DECK_GITHUB_CALLBACK_URI}.`,
    );
  }

  if (realqaApiOrigin !== CANONICAL_REALQA_AUDIENCE) {
    realqaIssues.push(
      `PUBLIC_REALQA_API_ORIGIN must be ${CANONICAL_REALQA_AUDIENCE}.`,
    );
  }
  if (realqaAudience !== CANONICAL_REALQA_AUDIENCE) {
    realqaIssues.push(
      `PUBLIC_REALQA_LOGTO_AUDIENCE must be ${CANONICAL_REALQA_AUDIENCE}.`,
    );
  }
  if (
    !realqaGitHubAppClientId ||
    realqaGitHubAppClientId.length > 255 ||
    /[\s:/]/.test(realqaGitHubAppClientId)
  ) {
    realqaIssues.push(
      "PUBLIC_REALQA_GITHUB_APP_CLIENT_ID is invalid.",
    );
  }
  if (
    !/^[a-z0-9](?:[a-z0-9-]{0,98}[a-z0-9])?$/.test(
      realqaGitHubAppSlug,
    )
  ) {
    realqaIssues.push(
      "PUBLIC_REALQA_GITHUB_APP_SLUG is invalid.",
    );
  }
  if (realqaGitHubCallbackUri !== CANONICAL_REALQA_CALLBACK) {
    realqaIssues.push(
      `PUBLIC_REALQA_GITHUB_CALLBACK_URI must be ${CANONICAL_REALQA_CALLBACK}.`,
    );
  }

  return {
    apiOrigin,
    appOrigin: browserOrigin,
    deck: {
      apiOrigin: deckApiOrigin,
      audience: deckAudience,
      githubAppClientId: deckGitHubAppClientId,
      githubAppSlug: deckGitHubAppSlug,
      githubCallbackUri: deckGitHubCallbackUri,
      issues: deckIssues,
    },
    logto: { endpoint, appId, audience },
    realqa: {
      apiOrigin: realqaApiOrigin,
      audience: realqaAudience,
      githubAppClientId: realqaGitHubAppClientId,
      githubAppSlug: realqaGitHubAppSlug,
      githubCallbackUri: realqaGitHubCallbackUri,
      issues: realqaIssues,
    },
    issues,
  };
}

export const runtimeConfig = readRuntimeConfig();
export const canonicalOrigin = CANONICAL_ORIGIN;
export const canonicalAudience = CANONICAL_AUDIENCE;
export const canonicalDeckAudience = CANONICAL_DECK_AUDIENCE;
export const canonicalDeckGitHubCallbackUri =
  CANONICAL_DECK_GITHUB_CALLBACK_URI;
export const canonicalRealQAAudience = CANONICAL_REALQA_AUDIENCE;
