import { Buffer } from "node:buffer";
import { fileURLToPath } from "node:url";
import { dirname, isAbsolute, relative, resolve, sep } from "node:path";

const moduleDirectory = dirname(fileURLToPath(import.meta.url));

export const repositoryRoot = resolve(moduleDirectory, "../..");
export const stateDirectory = resolve(repositoryRoot, ".dev-environment");
export const infisicalConfig = resolve(repositoryRoot, ".infisical.json");
export const composeFile = resolve(moduleDirectory, "compose.oss.yaml");
export const identityKeyFile = resolve(stateDirectory, "identity-hmac-key");
export const supportedInfisicalVersion = "0.43.116";
export const acceptedMarker = "__DEVHUD_CONFIGURATION_ACCEPTED__";
export const rejectedMarker = "__DEVHUD_CONFIGURATION_REJECTED__";
export const comparisonMarker = "__DEVHUD_CONFIGURATION_COMPARISON__";
export const comparisonKeyName = "DEVHUD_INTERNAL_CONFIGURATION_COMPARISON_KEY";
export const defaultOssLogtoIssuer = "http://localhost:3001/oidc";

export const modes = Object.freeze(["team", "oss"]);

export function requireMode(value = process.env.DEVHUD_LOCAL_MODE) {
  if (!modes.includes(value)) {
    throw new EnvironmentError(
      "mode.invalid",
      "development mode must be exactly one of: team, oss",
    );
  }
  return value;
}

export class EnvironmentError extends Error {
  constructor(code, message, names = []) {
    super(message);
    this.name = "EnvironmentError";
    this.code = code;
    this.names = [...names].sort();
  }
}

export function resolveInfisicalConfigPaths(source = process.env) {
  if (source.DEVHUD_ENVIRONMENT_TESTING !== "1") {
    return {
      projectConfigDirectory: repositoryRoot,
      configFile: infisicalConfig,
    };
  }
  const injectedDirectory = source.DEVHUD_TEST_INFISICAL_CONFIG_DIRECTORY;
  if (
    typeof injectedDirectory !== "string" ||
    injectedDirectory.trim() === "" ||
    !isAbsolute(injectedDirectory)
  ) {
    throw new EnvironmentError(
      "environment.test-config",
      "development environment tests require an absolute temporary Infisical config directory",
    );
  }
  const projectConfigDirectory = resolve(injectedDirectory);
  const relativePath = relative(repositoryRoot, projectConfigDirectory);
  const insideRepository =
    relativePath === "" ||
    (!relativePath.startsWith(`..${sep}`) &&
      relativePath !== ".." &&
      !isAbsolute(relativePath));
  if (insideRepository) {
    throw new EnvironmentError(
      "environment.test-config",
      "development environment tests require an Infisical config directory outside the checkout",
    );
  }
  return {
    projectConfigDirectory,
    configFile: resolve(projectConfigDirectory, ".infisical.json"),
  };
}

export function resolveOssLogtoIssuer(adminOverride, apiOverride) {
  const adminIssuer = adminOverride ?? defaultOssLogtoIssuer;
  const apiIssuer = apiOverride ?? defaultOssLogtoIssuer;
  if (adminIssuer !== apiIssuer) {
    throw new EnvironmentError(
      "environment.issuer-mismatch",
      "DevHud API and administrator OSS configuration must use the same issuer override",
      ["DEVHUD_LOGTO_ISSUER"],
    );
  }
  return adminIssuer;
}

export function resolveLocalStatePaths(source = process.env) {
  if (source.DEVHUD_ENVIRONMENT_TESTING !== "1") {
    return { stateDirectory, identityKeyFile };
  }
  const injectedDirectory = source.DEVHUD_TEST_STATE_DIRECTORY;
  if (
    typeof injectedDirectory !== "string" ||
    injectedDirectory.trim() === "" ||
    !isAbsolute(injectedDirectory)
  ) {
    throw new EnvironmentError(
      "environment.test-state",
      "development environment tests require an absolute temporary state directory",
    );
  }
  const resolvedDirectory = resolve(injectedDirectory);
  return {
    stateDirectory: resolvedDirectory,
    identityKeyFile: resolve(resolvedDirectory, "identity-hmac-key"),
  };
}

const localHttp = (value) => {
  if (!/^https?:\/\/[^/\\?#]+(?:[/?#]|$)/u.test(value)) return false;
  try {
    const parsed = new URL(value);
    if (
      parsed.username !== "" ||
      parsed.password !== "" ||
      parsed.search !== "" ||
      parsed.hash !== ""
    ) {
      return false;
    }
    return (
      parsed.protocol === "https:" ||
      (parsed.protocol === "http:" &&
        ["localhost", "127.0.0.1", "[::1]"].includes(parsed.hostname))
    );
  } catch {
    return false;
  }
};

const nonempty = (value) => typeof value === "string" && value.trim() !== "";
const url = (value) => nonempty(value) && localHttp(value);
const database = (value) => {
  try {
    const parsed = new URL(value);
    return ["postgres:", "postgresql:"].includes(parsed.protocol) && Boolean(parsed.hostname);
  } catch {
    return false;
  }
};
const hmacRing = (value) => {
  if (!nonempty(value)) return false;
  return value.split(",").every((entry) => {
    if (!/^[A-Za-z0-9+/]+={0,2}$/u.test(entry) || entry.length % 4 !== 0) return false;
    try {
      const decoded = Buffer.from(entry, "base64");
      return decoded.byteLength >= 32 && decoded.toString("base64") === entry;
    } catch {
      return false;
    }
  });
};
const assetBase = (value) => {
  if (!url(value)) return false;
  const parsed = new URL(value);
  return parsed.pathname === "/" && parsed.search === "" && parsed.hash === "";
};

export const apiContract = Object.freeze({
  service: "DevHud API",
  path: "/devhud/api",
  ossOverrideNames: Object.freeze([
    "DEVHUD_LOGTO_ISSUER",
    "DEVHUD_LOGTO_AUDIENCE",
    "DEVHUD_LOGTO_DESKTOP_CLIENT_ID",
    "DEVHUD_LOGTO_IOS_CLIENT_ID",
    "DEVHUD_LOGTO_ANDROID_CLIENT_ID",
    "DEVHUD_LOGTO_ADMIN_CLIENT_ID",
  ]),
  required: Object.freeze({
    DEVHUD_DATABASE_URL: database,
    DEVHUD_PUBLIC_API_URL: url,
    DEVHUD_LOGTO_ISSUER: url,
    DEVHUD_LOGTO_AUDIENCE: nonempty,
    DEVHUD_LOGTO_DESKTOP_CLIENT_ID: nonempty,
    DEVHUD_LOGTO_IOS_CLIENT_ID: nonempty,
    DEVHUD_LOGTO_ANDROID_CLIENT_ID: nonempty,
    DEVHUD_LOGTO_ADMIN_CLIENT_ID: nonempty,
    DEVHUD_ADMIN_REDIRECT_URI: (value) =>
      value === "http://localhost:46306/auth/callback",
    DEVHUD_PUBLIC_ASSET_BASE_URL: assetBase,
    DEVHUD_IDENTITY_HMAC_KEYS: hmacRing,
  }),
  optionalGroups: Object.freeze([
    Object.freeze([
      "DEVHUD_R2_ENDPOINT",
      "DEVHUD_R2_ACCESS_KEY_ID",
      "DEVHUD_R2_SECRET_ACCESS_KEY",
      "DEVHUD_R2_STAGING_BUCKET",
      "DEVHUD_R2_PUBLIC_BUCKET",
      "DEVHUD_CLOUDFLARE_API_TOKEN",
      "DEVHUD_CLOUDFLARE_ZONE_ID",
      "DEVHUD_CLOUDFLARE_RATE_LIMIT_RULE_ID",
    ]),
  ]),
  optionalValidators: Object.freeze({
    DEVHUD_R2_ENDPOINT: url,
    DEVHUD_R2_ACCESS_KEY_ID: nonempty,
    DEVHUD_R2_SECRET_ACCESS_KEY: nonempty,
    DEVHUD_R2_STAGING_BUCKET: nonempty,
    DEVHUD_R2_PUBLIC_BUCKET: nonempty,
    DEVHUD_CLOUDFLARE_API_TOKEN: nonempty,
    DEVHUD_CLOUDFLARE_ZONE_ID: nonempty,
    DEVHUD_CLOUDFLARE_RATE_LIMIT_RULE_ID: nonempty,
  }),
});

export const adminContract = Object.freeze({
  service: "DevHud administrator",
  path: "/devhud/admin",
  ossOverrideNames: Object.freeze(["DEVHUD_LOGTO_ISSUER"]),
  required: Object.freeze({ DEVHUD_LOGTO_ISSUER: url }),
  optionalGroups: Object.freeze([]),
  optionalValidators: Object.freeze({}),
});

export function validateInjectedEnvironment(contract, environment, baselineEnvironment = {}) {
  const baseline = new Map(Object.entries(baselineEnvironment));
  const requiredNames = Object.keys(contract.required);
  const optionalNames = contract.optionalGroups.flat();
  const allowed = new Set([...requiredNames, ...optionalNames]);
  const injectedNames = Object.keys(environment).filter(
    (name) =>
      (!baseline.has(name) || baseline.get(name) !== environment[name]),
  );
  const unknown = injectedNames.filter((name) => !allowed.has(name));
  if (unknown.length > 0) {
    throw new EnvironmentError(
      "environment.unknown",
      `${contract.service} configuration contains unknown names`,
      unknown,
    );
  }

  const missing = requiredNames.filter((name) => !nonempty(environment[name]));
  if (missing.length > 0) {
    throw new EnvironmentError(
      "environment.missing",
      `${contract.service} configuration is missing required names`,
      missing,
    );
  }

  for (const group of contract.optionalGroups) {
    const present = group.filter((name) => nonempty(environment[name]));
    if (present.length !== 0 && present.length !== group.length) {
      throw new EnvironmentError(
        "environment.partial-group",
        `${contract.service} optional upload configuration must be all present or all absent`,
        group.filter((name) => !present.includes(name)),
      );
    }
  }

  const invalid = requiredNames.filter(
    (name) => !contract.required[name](environment[name]),
  );
  for (const group of contract.optionalGroups) {
    if (group.every((name) => nonempty(environment[name]))) {
      invalid.push(
        ...group.filter((name) => !contract.optionalValidators[name](environment[name])),
      );
    }
  }
  if (invalid.length > 0) {
    throw new EnvironmentError(
      "environment.invalid-values",
      `${contract.service} configuration has invalid values`,
      invalid,
    );
  }

  return Object.fromEntries(
    [...requiredNames, ...optionalNames]
      .filter((name) => nonempty(environment[name]))
      .map((name) => [name, environment[name]]),
  );
}

export function formatEnvironmentError(error) {
  if (!(error instanceof EnvironmentError)) return error.message;
  const suffix = error.names.length > 0 ? `: ${error.names.join(", ")}` : "";
  return `[${error.code}] ${error.message}${suffix}`;
}
