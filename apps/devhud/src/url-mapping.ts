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
  readonly port: string;
  readonly path: readonly string[];
}

export class UrlMappingError extends TypeError {}

const literalScheme = /^(?:https?|\*)$/u;
const literalHost = /^(?:[A-Za-z0-9-]+|\*)$/u;
const literalPort = /^(?:[1-9]\d{0,4}|\*)?$/u;

/** Parse a URL glob without allowing URL credentials, query, or fragment data. */
export function parseUrlPattern(value: string): ParsedUrlPattern {
  if (value !== value.trim() || value.includes("?") || value.includes("#") || value.includes("@")) throw new UrlMappingError("pattern must not contain credentials, query, or fragment");
  const match = /^(\*|https?):\/\/([^/:]+)(?::([^/]*))?(\/.*)?$/u.exec(value);
  if (!match) throw new UrlMappingError("pattern must contain scheme, host, optional port, and path");
  const [, scheme, hostText, port = "", pathText = "/"] = match;
  if (!literalScheme.test(scheme) || !literalPort.test(port) || (port !== "" && port !== "*" && Number(port) > 65535)) throw new UrlMappingError("pattern has an invalid scheme or port");
  const host = hostText.split(".");
  if (host.some((part) => !literalHost.test(part) || part === "")) throw new UrlMappingError("host labels must be literals or *");
  const path = pathText.split("/").slice(1);
  if (path.some((part) => part === "" && path.length > 1) || path.some((part) => part.includes("*") && part !== "*" && part !== "**")) throw new UrlMappingError("path wildcards must occupy a complete segment");
  return { scheme, host, port, path: path.length === 1 && path[0] === "" ? [] : path };
}

export function parseLiveUrl(value: string): ParsedUrlPattern {
  try {
    const url = new URL(value);
    if ((url.protocol !== "http:" && url.protocol !== "https:") || !url.hostname) throw new Error();
    return { scheme: url.protocol.slice(0, -1), host: url.hostname.split("."), port: url.port, path: url.pathname.split("/").slice(1).filter(Boolean) };
  } catch {
    throw new UrlMappingError("live URL must be an HTTP(S) URL");
  }
}

export function mappingMatches(mapping: Pick<UrlRepositoryMapping, "pattern">, value: string): boolean {
  const pattern = parseUrlPattern(mapping.pattern);
  const url = parseLiveUrl(value);
  return componentMatches(pattern.scheme, url.scheme, false)
    && componentMatches(pattern.port, url.port, false)
    && pattern.host.length === url.host.length
    && pattern.host.every((part, index) => componentMatches(part, url.host[index] ?? "", true))
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
  return matched.toSorted(compareMappings)[0] ?? null;
}

export type RepositorySelection =
  | { readonly kind: "matched"; readonly mapping: UrlRepositoryMapping }
  | { readonly kind: "manual-required" };

/** A live URL may only preselect a configured repository; it never creates one. */
export function resolveRepositorySelection(mappings: readonly UrlRepositoryMapping[], liveUrl: string | null): RepositorySelection {
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
  for (let index = 0; index < mappings.length; index += 1) for (let other = index + 1; other < mappings.length; other += 1) {
    const first = mappings[index]; const second = mappings[other];
    if (first && second && patternsOverlap(parseUrlPattern(first.pattern), parseUrlPattern(second.pattern))) overlaps.push({ first, second });
  }
  return overlaps;
}

function componentMatches(pattern: string, value: string, insensitive: boolean): boolean {
  return pattern === "*" || (insensitive ? pattern.toLowerCase() === value.toLowerCase() : pattern === value);
}

function pathMatches(pattern: readonly string[], value: readonly string[], patternIndex = 0, valueIndex = 0): boolean {
  while (patternIndex < pattern.length) {
    const component = pattern[patternIndex];
    if (component === "**") {
      if (patternIndex === pattern.length - 1) return true;
      for (let next = valueIndex; next <= value.length; next += 1) if (pathMatches(pattern, value, patternIndex + 1, next)) return true;
      return false;
    }
    if (valueIndex >= value.length || !componentMatches(component ?? "", value[valueIndex] ?? "", false)) return false;
    patternIndex += 1; valueIndex += 1;
  }
  return valueIndex === value.length;
}

function patternsOverlap(left: ParsedUrlPattern, right: ParsedUrlPattern): boolean {
  return componentsOverlap(left.scheme, right.scheme, false)
    && componentsOverlap(left.port, right.port, false)
    && left.host.length === right.host.length
    && left.host.every((part, index) => componentsOverlap(part, right.host[index] ?? "", true))
    && pathsOverlap(left.path, right.path);
}

function componentsOverlap(left: string, right: string, insensitive: boolean): boolean {
  return left === "*" || right === "*" || (insensitive ? left.toLowerCase() === right.toLowerCase() : left === right);
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
