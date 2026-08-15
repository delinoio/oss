const remoteUrl = String.raw`(?:https?:)?\/\/`;

const executableRemoteLoadPatterns = [
  new RegExp(
    String.raw`<(?:script|link|img|iframe)[^>]+(?:src|href)\s*=\s*["']${remoteUrl}`,
    "iu",
  ),
  new RegExp(String.raw`\b(?:fetch|import)\s*\(\s*["']${remoteUrl}`, "iu"),
  new RegExp(String.raw`(?:@import\s+["']?|url\(\s*["']?)${remoteUrl}`, "iu"),
];

export function hasExecutableRemoteLoad(text) {
  return executableRemoteLoadPatterns.some((pattern) => pattern.test(text));
}

export function hasExactCspDirectiveSources(policy, directiveName, expectedSources) {
  const matchingDirectives = policy
    .split(";")
    .map((directive) => directive.trim())
    .filter(Boolean)
    .map((directive) => directive.split(/\s+/u))
    .filter(([name]) => name === directiveName);

  return (
    matchingDirectives.length === 1 &&
    matchingDirectives[0].length === expectedSources.length + 1 &&
    expectedSources.every((source, index) => matchingDirectives[0][index + 1] === source)
  );
}
