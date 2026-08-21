export interface ConfiguredUrlMatcher {
  readonly scheme: string;
  readonly host: readonly string[];
  readonly hostIsIpLiteral: boolean;
  readonly port: string;
  readonly path: readonly string[];
}

export interface ConfiguredMapping {
  readonly mappingId: string;
  readonly matcher: ConfiguredUrlMatcher;
}

export interface ConfiguredOrigin {
  readonly origin: string;
  readonly mappings: readonly ConfiguredMapping[];
}

export interface ExtensionConfiguration {
  readonly origins?: readonly ConfiguredOrigin[];
  readonly language?: "en" | "ko";
}

interface ParsedLiveUrl {
  readonly scheme: string;
  readonly host: readonly string[];
  readonly hostIsIpLiteral: boolean;
  readonly port: string;
  readonly path: readonly string[];
}

export function selectConfiguredMapping(configuration: ExtensionConfiguration, value: string): string | null {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return null;
  }
  const configured = configuration.origins?.find((candidate) => candidate.origin === url.origin);
  return configured?.mappings.find((mapping) => matcherMatches(mapping.matcher, value))?.mappingId ?? null;
}

export function matcherMatches(matcher: ConfiguredUrlMatcher, value: string): boolean {
  let url: ParsedLiveUrl;
  try {
    url = parseLiveUrl(value);
  } catch {
    return false;
  }
  return componentMatches(matcher.scheme, url.scheme)
    && componentMatches(normalizeDefaultPort(url.scheme, matcher.port), url.port)
    && matcher.host.length === url.host.length
    && matcher.host.every((part, index) => hostComponentMatches(part, url.host[index] ?? "", matcher.hostIsIpLiteral, url.hostIsIpLiteral))
    && pathMatches(matcher.path, url.path);
}

function parseLiveUrl(value: string): ParsedLiveUrl {
  const url = new URL(value);
  if ((url.protocol !== "http:" && url.protocol !== "https:") || !url.hostname) throw new TypeError("unsupported URL");
  const scheme = url.protocol.slice(0, -1);
  return {
    scheme,
    host: splitDnsLabels(url.hostname),
    hostIsIpLiteral: isIpLiteral(url.hostname),
    port: normalizeDefaultPort(scheme, url.port),
    path: url.pathname === "/" ? [] : url.pathname.slice(1).split("/").map(canonicalizeLiteralSegment),
  };
}

function splitDnsLabels(host: string): string[] {
  const labels = host.split(".");
  if (labels.length > 1 && labels[labels.length - 1] === "") labels.pop();
  return labels;
}

function normalizeDefaultPort(scheme: string, port: string): string {
  return (scheme === "http" && port === "80") || (scheme === "https" && port === "443") ? "" : port;
}

function canonicalizeLiteralSegment(value: string): string {
  try {
    return decodeURIComponent(value).replace(/%/gu, "%25").replace(/\*/gu, "%2A");
  } catch {
    return value.replace(/%[0-9a-f]{2}/giu, (escape) => escape.toUpperCase());
  }
}

function componentMatches(pattern: string, value: string): boolean {
  return pattern === "*" || pattern === value;
}

function hostComponentMatches(pattern: string, value: string, patternIsIpLiteral: boolean, valueIsIpLiteral: boolean): boolean {
  if (pattern === "*") return !patternIsIpLiteral && !valueIsIpLiteral;
  return pattern.toLowerCase() === value.toLowerCase();
}

function isIpLiteral(host: string): boolean {
  return host.startsWith("[") || /^\d{1,3}(?:\.\d{1,3}){3}$/u.test(host);
}

function pathMatches(pattern: readonly string[], value: readonly string[]): boolean {
  let current = new Set<number>([0]);
  const expandGlobstars = (states: Set<number>): Set<number> => {
    const expanded = new Set(states);
    const pending = [...states];
    while (pending.length > 0) {
      const index = pending.pop()!;
      if (pattern[index] === "**" && !expanded.has(index + 1)) {
        expanded.add(index + 1);
        pending.push(index + 1);
      }
    }
    return expanded;
  };
  current = expandGlobstars(current);
  for (const segment of value) {
    const next = new Set<number>();
    for (const index of current) {
      const component = pattern[index];
      if (component === "**") next.add(index);
      else if (component !== undefined && componentMatches(component, segment)) next.add(index + 1);
    }
    current = expandGlobstars(next);
    if (current.size === 0) return false;
  }
  return expandGlobstars(current).has(pattern.length);
}
