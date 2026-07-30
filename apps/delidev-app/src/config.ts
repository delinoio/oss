const CANONICAL_ORIGIN = "https://deli.dev";
const CANONICAL_AUDIENCE = "https://delibase.deli.dev";
const CANONICAL_REALQA_AUDIENCE = "https://realqa.deli.dev";
const CANONICAL_REALQA_CALLBACK =
  "https://realqa.deli.dev/github/oauth/callback";

export interface PublicEnvironment {
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

export function readRuntimeConfig(
  environment: PublicEnvironment = publicEnvironment,
  browserOrigin =
    typeof window === "undefined" ? CANONICAL_ORIGIN : window.location.origin,
): RuntimeConfig {
  const apiOrigin = environment.PUBLIC_DELIBASE_API_ORIGIN?.trim() ?? "";
  const endpoint = environment.PUBLIC_LOGTO_ENDPOINT?.trim() ?? "";
  const appId = environment.PUBLIC_LOGTO_APP_ID?.trim() ?? "";
  const audience = environment.PUBLIC_LOGTO_AUDIENCE?.trim() ?? "";
  const realqaApiOrigin =
    environment.PUBLIC_REALQA_API_ORIGIN?.trim() ?? "";
  const realqaAudience =
    environment.PUBLIC_REALQA_LOGTO_AUDIENCE?.trim() ?? "";
  const githubAppClientId =
    environment.PUBLIC_REALQA_GITHUB_APP_CLIENT_ID?.trim() ?? "";
  const githubAppSlug =
    environment.PUBLIC_REALQA_GITHUB_APP_SLUG?.trim() ?? "";
  const githubCallbackUri =
    environment.PUBLIC_REALQA_GITHUB_CALLBACK_URI?.trim() ?? "";
  const issues: string[] = [];
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
    !githubAppClientId ||
    githubAppClientId.length > 255 ||
    /[\s:/]/.test(githubAppClientId)
  ) {
    realqaIssues.push(
      "PUBLIC_REALQA_GITHUB_APP_CLIENT_ID is invalid.",
    );
  }
  if (
    !/^[a-z0-9](?:[a-z0-9-]{0,98}[a-z0-9])?$/.test(
      githubAppSlug,
    )
  ) {
    realqaIssues.push(
      "PUBLIC_REALQA_GITHUB_APP_SLUG is invalid.",
    );
  }
  if (githubCallbackUri !== CANONICAL_REALQA_CALLBACK) {
    realqaIssues.push(
      `PUBLIC_REALQA_GITHUB_CALLBACK_URI must be ${CANONICAL_REALQA_CALLBACK}.`,
    );
  }

  return {
    apiOrigin,
    appOrigin: browserOrigin,
    logto: { endpoint, appId, audience },
    realqa: {
      apiOrigin: realqaApiOrigin,
      audience: realqaAudience,
      githubAppClientId,
      githubAppSlug,
      githubCallbackUri,
      issues: realqaIssues,
    },
    issues,
  };
}

export const runtimeConfig = readRuntimeConfig();
export const canonicalOrigin = CANONICAL_ORIGIN;
export const canonicalAudience = CANONICAL_AUDIENCE;
export const canonicalRealQAAudience = CANONICAL_REALQA_AUDIENCE;
