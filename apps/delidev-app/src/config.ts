const CANONICAL_ORIGIN = "https://deli.dev";
const CANONICAL_AUDIENCE = "https://delibase.deli.dev";

interface PublicEnvironment {
  readonly PUBLIC_DELIBASE_API_ORIGIN?: string;
  readonly PUBLIC_LOGTO_ENDPOINT?: string;
  readonly PUBLIC_LOGTO_APP_ID?: string;
  readonly PUBLIC_LOGTO_AUDIENCE?: string;
}

const publicEnvironment = import.meta.env as PublicEnvironment;

export interface RuntimeConfig {
  apiOrigin: string;
  appOrigin: string;
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

export function readRuntimeConfig(
  environment: PublicEnvironment = publicEnvironment,
  browserOrigin =
    typeof window === "undefined" ? CANONICAL_ORIGIN : window.location.origin,
): RuntimeConfig {
  const apiOrigin = environment.PUBLIC_DELIBASE_API_ORIGIN?.trim() ?? "";
  const endpoint = environment.PUBLIC_LOGTO_ENDPOINT?.trim() ?? "";
  const appId = environment.PUBLIC_LOGTO_APP_ID?.trim() ?? "";
  const audience = environment.PUBLIC_LOGTO_AUDIENCE?.trim() ?? "";
  const issues: string[] = [];

  if (!validHttpsUrl(apiOrigin)) {
    issues.push("PUBLIC_DELIBASE_API_ORIGIN must be an HTTPS URL.");
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

  return {
    apiOrigin,
    appOrigin: browserOrigin,
    logto: { endpoint, appId, audience },
    issues,
  };
}

export const runtimeConfig = readRuntimeConfig();
export const canonicalOrigin = CANONICAL_ORIGIN;
export const canonicalAudience = CANONICAL_AUDIENCE;
