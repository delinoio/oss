import { decodeHTML } from "entities";

// Preserve the public-content checks that protected these guides before their
// move from public-docs. Route exceptions belong to this standalone site.
const forbiddenCredentials = [
  /(?:GH_TOKEN|DEVHUD_[A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|KEY)|Authorization:\s*Bearer)/iu,
  /(?:"(?:token|access[_-]?token|refresh[_-]?token|api[_-]?key)"|(?:token|access[_-]?token|refresh[_-]?token|api[_-]?key))\s*[:=]\s*[^\s<`]+/iu,
  /(?:"(?:private key|signing key|password|access key|secret)"|(?:private key|signing key|password|access key|secret))\s*[:=]\s*[^\s<`]+/iu,
  /\b(?:ghp|github_pat)_[A-Za-z0-9_]+\b/iu,
];
const credentialParameter = /^(?:token|access[_-]?token|refresh[_-]?token|api[_-]?key|password|secret|code|oauth_code)$/iu;
const urlAttributePattern = /\b(?:href|src|srcset|poster|action|formaction|data)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'`=<>]+))/giu;
const cssUrlPattern = /\burl\s*\(\s*(?:"([^"]*)"|'([^']*)'|([^\s)]+))\s*\)/giu;
const cssImportPattern = /@import\s+(?:"([^"]*)"|'([^']*)')/giu;

function attributeValue(match) {
  return match[1] ?? match[2] ?? match[3] ?? "";
}

function resourceTargets(contents) {
  const targets = [];
  for (const match of contents.matchAll(urlAttributePattern)) {
    const value = attributeValue(match);
    if (/\bsrcset\s*=/iu.test(match[0])) {
      for (const candidate of value.split(",")) {
        const target = candidate.trim().split(/\s+/u, 1)[0];
        if (target) targets.push(target);
      }
    } else {
      targets.push(value.trim());
    }
  }
  for (const match of contents.matchAll(cssUrlPattern)) targets.push(attributeValue(match).trim());
  for (const match of contents.matchAll(cssImportPattern)) targets.push(attributeValue(match).trim());
  return targets;
}

function hasCredentialParameters(parameters) {
  for (const [key, value] of parameters) {
    if (credentialParameter.test(key)
      || forbiddenCredentials.some((pattern) => pattern.test(`${key}=${value}`))) return true;
  }
  return false;
}

function hasCredentialFragment(url) {
  const fragment = url.hash.slice(1).replace(/^\?/u, "");
  if (hasCredentialParameters(new URLSearchParams(fragment))) return true;
  // Hash routers put the query after a route, while OAuth can also return
  // parameters directly in the fragment. Check both forms independently.
  const queryStart = fragment.indexOf("?");
  return queryStart !== -1
    && hasCredentialParameters(new URLSearchParams(fragment.slice(queryStart + 1)));
}

export function createPublicContentValidator(routeIds) {
  const routes = [...routeIds];
  const routePattern = routes.filter((route) => route !== "/")
    .sort((left, right) => right.length - left.length)
    .map((route) => route.slice(1).replace(/[.*+?^${}()|[\]\\]/gu, "\\$&"))
    .join("|");
  const htmlRoutes = new Set(routes.map((route) => route === "/" ? "/index.html" : `${route}.html`));
  const forbiddenPaths = [
    new RegExp(`(?:^|[\\s("'\\x60>])/(?!(?:${routePattern})(?:\\.html)?(?:[?#"'\\x60<\\s]|$))[A-Za-z0-9._~-]+(?:[/\\\\][^\\s"'\\x60<>]*)?`, "u"),
    /(?:^|[\s("'`>])(?:\.\.[\\/])+(?:[A-Za-z0-9._~-]+[\\/])+[^\s"'`<>]*/u,
    /(?:^|[\s("'`>])(?:apps|cmds|crates|docs|packaging|packages|protos|scripts|servers)(?:[\\/][^\s"'`<>]+)+/u,
    /(?:^|[\s("'`>])[A-Za-z]:[\\/][^\s"'`<>]*/u,
    /(?:^|[\s("'`>])\\\\[^\s"'`<>\\/]+[\\/][^\s"'`<>]*/u,
    /(?:^|[\s("'`>])file:\/\/(?:[^\/\s"'`<>]+)?\/[^\s"'`<>]*/iu,
  ];

  return function containsProhibitedPublicContent(contents, pageUrl) {
    const renderedText = decodeHTML(contents
      .replace(/<(?:script|style)\b[^>]*>[\s\S]*?<\/(?:script|style)>/giu, " ")
      .replace(/<[^>]*>/gu, " ")).replace(/\s+/gu, " ");
    const comments = decodeHTML([...contents.matchAll(/<!--([\s\S]*?)-->/gu)]
      .map(([, comment]) => comment).join(" "));
    if (forbiddenCredentials.some((pattern) => pattern.test(contents) || pattern.test(renderedText) || pattern.test(comments))) return true;
    if (forbiddenPaths.some((pattern) => pattern.test(renderedText) || pattern.test(comments))) return true;

    for (const target of resourceTargets(contents)) {
      const decoded = decodeHTML(target);
      let url;
      try {
        url = new URL(decoded, pageUrl);
      } catch {
        // Malformed URLs are not credential evidence, but still check raw paths.
      }
      if (url && (url.username || url.password || hasCredentialParameters(url.searchParams)
        || hasCredentialFragment(url))) return true;
      const sameOrigin = url?.origin === pageUrl.origin;
      if (sameOrigin && /^\/(?:assets|static)\//u.test(url.pathname)) continue;
      if (forbiddenPaths.some((pattern) => pattern.test(decoded))) return true;
      if (sameOrigin && !htmlRoutes.has(url.pathname)) {
        let decodedPath;
        try {
          decodedPath = decodeURIComponent(url.pathname);
        } catch {
          continue;
        }
        if (forbiddenPaths.some((pattern) => pattern.test(decodedPath))) return true;
      }
    }
    return false;
  };
}
