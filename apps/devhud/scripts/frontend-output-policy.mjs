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
