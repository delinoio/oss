export interface UrlRepositoryMapping {
  readonly id: string;
  readonly pattern: string;
  readonly repository: { readonly owner: string; readonly name: string };
  readonly credentialProfileRef: string;
  readonly priority: number;
  readonly chromeOrigin: string | null;
  readonly updatedAt: string;
}

export interface ParsedUrlPattern {
  readonly scheme: string;
  readonly host: readonly string[];
  readonly hostIsIpLiteral: boolean;
  readonly port: string;
  readonly path: readonly string[];
}

export class UrlMappingError extends TypeError {}

const literalScheme = /^(?:https?|\*)$/u;
const literalPort = /^(?:[1-9]\d{0,4}|\*)?$/u;

/** Parse a URL glob without allowing URL credentials, query, or fragment data. */
export function parseUrlPattern(value: string): ParsedUrlPattern {
  if (value !== value.trim() || value.includes("?") || value.includes("#")) throw new UrlMappingError("pattern must not contain credentials, query, or fragment");
  const match = /^(\*|https?):\/\/(\[[^\]]+\]|[^/:]+)(?::([^/]*))?(\/.*)?$/u.exec(value);
  if (!match) throw new UrlMappingError("pattern must contain scheme, host, optional port, and path");
  const [, scheme, hostText, port = "", pathText = "/"] = match;
  if (hostText.includes("@") || hostText.includes("\\")) throw new UrlMappingError("pattern must not contain credentials or backslashes");
  if (!literalScheme.test(scheme) || !literalPort.test(port) || (port !== "" && port !== "*" && Number(port) > 65535)) throw new UrlMappingError("pattern has an invalid scheme or port");
  const { host, hostIsIpLiteral } = parsePatternHost(hostText);
  const path = pathText.split("/").slice(1);
  if (path.some((part) => part.includes("*") && part !== "*" && part !== "**")) throw new UrlMappingError("path wildcards must occupy a complete segment");
  return { scheme, host, hostIsIpLiteral, port: normalizeDefaultPort(scheme, port), path: canonicalizePatternPath(pathText, path) };
}

function parsePatternHost(hostText: string): Pick<ParsedUrlPattern, "host" | "hostIsIpLiteral"> {
  if (hostText.startsWith("[")) {
    try {
      // URL canonicalization keeps an IPv6 literal as one bracketed host component.
      return { host: [new URL(`http://${hostText}/`).hostname], hostIsIpLiteral: true };
    } catch {
      throw new UrlMappingError("host must be a valid bracketed IPv6 literal");
    }
  }
  const labels = hostText.split(".");
  if (labels.some((part) => part === "" || (part.includes("*") && part !== "*"))) throw new UrlMappingError("host labels must be literals or *");
  const canonicalInput = labels.map((part, index) => {
    if (part !== "*") return part;
    return `devhud-wildcard-${index}`;
  }).join(".");
  try {
    const canonicalHost = new URL(`http://${canonicalInput}/`).hostname;
    const canonicalLabels = canonicalHost.split(".");
    if (!labels.includes("*")) {
      if (canonicalLabels.some((part) => part.includes("*"))) throw new UrlMappingError("host labels must be literals or *");
      return { host: canonicalLabels, hostIsIpLiteral: isIpv4Literal(canonicalHost) };
    }
    if (canonicalLabels.length !== labels.length || canonicalLabels.some((part, index) => labels[index] !== "*" && part.includes("*"))) throw new UrlMappingError("host labels must be literals or *");
    return { host: canonicalLabels.map((part, index) => labels[index] === "*" ? "*" : part), hostIsIpLiteral: false };
  } catch {
    throw new UrlMappingError("host labels must be literals or *");
  }
}

export function parseLiveUrl(value: string): ParsedUrlPattern {
  try {
    const url = new URL(value);
    if ((url.protocol !== "http:" && url.protocol !== "https:") || !url.hostname) throw new Error();
    const scheme = url.protocol.slice(0, -1);
    return { scheme, host: url.hostname.split("."), hostIsIpLiteral: isIpLiteral(url.hostname), port: normalizeDefaultPort(scheme, url.port), path: canonicalizeLiteralPath(url.pathname) };
  } catch {
    throw new UrlMappingError("live URL must be an HTTP(S) URL");
  }
}

function normalizeDefaultPort(scheme: string, port: string): string {
  return (scheme === "http" && port === "80") || (scheme === "https" && port === "443") ? "" : port;
}

function canonicalizePatternPath(pathText: string, path: readonly string[]): readonly string[] {
  // Shield glob syntax while the URL parser normalizes only literal path semantics.
  const markers = new Map<string, "*" | "**">();
  const rawPath = path.map((part, index) => {
    if (part !== "*" && part !== "**") return part;
    let marker = `__devhud_wildcard_${index}__`;
    while (path.includes(marker)) marker = `_${marker}`;
    markers.set(marker, part);
    return marker;
  });
  const normalized = new URL(`https://example.invalid/${rawPath.join("/")}`).pathname;
  return normalized === "/" ? [] : normalized.slice(1).split("/").map((part) => markers.get(part) ?? canonicalizeLiteralSegment(part));
}

function canonicalizeLiteralPath(pathname: string): readonly string[] {
  return pathname === "/" ? [] : pathname.slice(1).split("/").map(canonicalizeLiteralSegment);
}

function canonicalizeLiteralSegment(value: string): string {
  try {
    const decoded = decodeURIComponent(value);
    // Escape only percent and wildcard text so literal serialized escapes remain distinguishable.
    return decoded.replace(/%/gu, "%25").replace(/\*/gu, "%2A");
  } catch {
    // WHATWG URLs preserve escaped bytes that are not valid UTF-8, so retain them
    // while canonicalizing their serialized escape spelling for stable matching.
    return value.replace(/%[0-9a-f]{2}/giu, (escape) => escape.toUpperCase());
  }
}

export function mappingMatches(mapping: Pick<UrlRepositoryMapping, "pattern">, value: string): boolean {
  const pattern = parseUrlPattern(mapping.pattern);
  const url = parseLiveUrl(value);
  return componentMatches(pattern.scheme, url.scheme, false)
    && componentMatches(normalizeDefaultPort(url.scheme, pattern.port), url.port, false)
    && pattern.host.length === url.host.length
    && pattern.host.every((part, index) => hostComponentMatches(part, url.host[index] ?? "", pattern.hostIsIpLiteral, url.hostIsIpLiteral))
    && pathMatches(pattern.path, url.path);
}

export function literalSpecificity(mapping: Pick<UrlRepositoryMapping, "pattern">): number {
  const pattern = parseUrlPattern(mapping.pattern);
  return [pattern.scheme, pattern.port, ...pattern.host, ...pattern.path]
    .filter((component) => component !== "*" && component !== "**")
    .reduce((total, component) => total + component.length, 0);
}

export function selectUrlMapping(mappings: readonly UrlRepositoryMapping[], liveUrl: string | null): UrlRepositoryMapping | null {
  if (liveUrl === null) return null;
  const matched = mappings.filter((mapping) => mappingMatches(mapping, liveUrl));
  return matched.sort(compareMappings)[0] ?? null;
}

export type RepositorySelection =
  | { readonly kind: "matched"; readonly mapping: UrlRepositoryMapping }
  | { readonly kind: "manual-required" };

/** A live URL may only preselect a configured repository; it never creates one. */
export function resolveRepositorySelection(mappings: readonly UrlRepositoryMapping[], liveUrl: string | null): RepositorySelection {
  if (liveUrl === null) return { kind: "manual-required" };
  try { parseLiveUrl(liveUrl); } catch { return { kind: "manual-required" }; }
  const mapping = selectUrlMapping(mappings, liveUrl);
  return mapping === null ? { kind: "manual-required" } : { kind: "matched", mapping };
}

export function compareMappings(left: UrlRepositoryMapping, right: UrlRepositoryMapping): number {
  return right.priority - left.priority
    || literalSpecificity(right) - literalSpecificity(left)
    || Date.parse(right.updatedAt) - Date.parse(left.updatedAt)
    || left.id.localeCompare(right.id);
}

export function findMappingOverlaps(mappings: readonly UrlRepositoryMapping[]): readonly { readonly first: UrlRepositoryMapping; readonly second: UrlRepositoryMapping }[] {
  const overlaps: { first: UrlRepositoryMapping; second: UrlRepositoryMapping }[] = [];
  const parsed = mappings.map((mapping) => ({ mapping, pattern: parseUrlPattern(mapping.pattern) }));
  for (let index = 0; index < parsed.length; index += 1) for (let other = index + 1; other < parsed.length; other += 1) {
    const first = parsed[index]; const second = parsed[other];
    if (first && second && patternsOverlap(first.pattern, second.pattern)) overlaps.push({ first: first.mapping, second: second.mapping });
  }
  return overlaps;
}

function componentMatches(pattern: string, value: string, insensitive: boolean): boolean {
  return pattern === "*" || (insensitive ? pattern.toLowerCase() === value.toLowerCase() : pattern === value);
}

function hostComponentMatches(pattern: string, value: string, patternIsIpLiteral: boolean, valueIsIpLiteral: boolean): boolean {
  if (pattern === "*") return !patternIsIpLiteral && !valueIsIpLiteral;
  return componentMatches(pattern, value, true);
}

function isIpLiteral(host: string): boolean {
  return host.startsWith("[") || isIpv4Literal(host);
}

function isIpv4Literal(host: string): boolean {
  return /^\d{1,3}(?:\.\d{1,3}){3}$/u.test(host);
}

function pathMatches(pattern: readonly string[], value: readonly string[], patternIndex = 0, valueIndex = 0): boolean {
  const memo = new Map<string, boolean>();
  const visit = (currentPatternIndex: number, currentValueIndex: number): boolean => {
    const key = `${currentPatternIndex}:${currentValueIndex}`;
    const previous = memo.get(key); if (previous !== undefined) return previous;
    let result: boolean;
    const component = pattern[currentPatternIndex];
    if (currentPatternIndex === pattern.length) result = currentValueIndex === value.length;
    else if (component === "**") result = visit(currentPatternIndex + 1, currentValueIndex) || (currentValueIndex < value.length && visit(currentPatternIndex, currentValueIndex + 1));
    else result = currentValueIndex < value.length && componentMatches(component ?? "", value[currentValueIndex] ?? "", false) && visit(currentPatternIndex + 1, currentValueIndex + 1);
    memo.set(key, result); return result;
  };
  return visit(patternIndex, valueIndex);
}

function patternsOverlap(left: ParsedUrlPattern, right: ParsedUrlPattern): boolean {
  return ["http", "https"].some((scheme) => componentsOverlap(left.scheme, scheme, false)
    && componentsOverlap(right.scheme, scheme, false)
    && componentsOverlap(normalizeDefaultPort(scheme, left.port), normalizeDefaultPort(scheme, right.port), false))
    && left.host.length === right.host.length
    && left.host.every((part, index) => hostComponentsOverlap(part, right.host[index] ?? "", left.hostIsIpLiteral, right.hostIsIpLiteral))
    && pathsOverlap(left.path, right.path);
}

function componentsOverlap(left: string, right: string, insensitive: boolean): boolean {
  return left === "*" || right === "*" || (insensitive ? left.toLowerCase() === right.toLowerCase() : left === right);
}

function hostComponentsOverlap(left: string, right: string, leftIsIpLiteral: boolean, rightIsIpLiteral: boolean): boolean {
  if (left === "*") return !leftIsIpLiteral && !rightIsIpLiteral;
  if (right === "*") return !leftIsIpLiteral && !rightIsIpLiteral;
  return componentsOverlap(left, right, true);
}

function pathsOverlap(left: readonly string[], right: readonly string[]): boolean {
  const memo = new Map<string, boolean>();
  const visit = (leftIndex: number, rightIndex: number): boolean => {
    const key = `${leftIndex}:${rightIndex}`;
    const previous = memo.get(key); if (previous !== undefined) return previous;
    let result: boolean;
    if (leftIndex === left.length && rightIndex === right.length) result = true;
    else if (left[leftIndex] === "**") result = visit(leftIndex + 1, rightIndex) || (rightIndex < right.length && visit(leftIndex, rightIndex + 1));
    else if (right[rightIndex] === "**") result = visit(leftIndex, rightIndex + 1) || (leftIndex < left.length && visit(leftIndex + 1, rightIndex));
    else result = leftIndex < left.length && rightIndex < right.length && componentsOverlap(left[leftIndex] ?? "", right[rightIndex] ?? "", false) && visit(leftIndex + 1, rightIndex + 1);
    memo.set(key, result); return result;
  };
  return visit(0, 0);
}
