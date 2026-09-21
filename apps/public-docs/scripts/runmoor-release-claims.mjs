// Preserve the availability classifier and negation semantics that protected
// Runmoor guides before they moved from public-docs. Stable-channel claims remain supported.
const releaseAvailabilityClaim =
  /\b(?:partial|staged)\s+(?:GA|availability|general[- ]availability)\b|\b(?:partial|staged)\s+or\s+(?:staged|partial)\s+general[- ]availability\b|\b(?:beta|phased|fractional)\s+(?:GA|availability|general[- ]availability|rollout|channel)\b|\bearly[- ]access(?:\s+(?:GA|availability|general[- ]availability|rollout|channel))?\b|\bearly announcement\b/giu;
const negativeAvailabilityPredicate =
  /^\s*(?:(?:is|are|remains?|remain)\s+(?:(?:not|never)\s+(?:available|supported|permitted|allowed|excluded|unsupported)|(?:unavailable|unsupported|excluded))|isn't\s+(?:available|supported|permitted|allowed|excluded|unsupported)|will\s+not\s+(?:be\s+)?(?:available|supported|permitted|allowed|excluded|unsupported))\b/iu;
const unnegatedProhibitionPredicate =
  /^\s*(?:is|are|remains?|remain)\s+(?:prohibited|forbidden|disallowed)\b/iu;

export function containsAffirmativeReleaseClaim(contents) {
  for (const match of contents.matchAll(releaseAvailabilityClaim)) {
    const sentenceStart = Math.max(
      contents.lastIndexOf(".", match.index) + 1,
      contents.lastIndexOf("!", match.index) + 1,
      contents.lastIndexOf("?", match.index) + 1,
    );
    const sentenceEndOffset = contents
      .slice(match.index + match[0].length)
      .search(/[.!?]/u);
    const sentenceEnd =
      sentenceEndOffset === -1
        ? -1
        : match.index + match[0].length + sentenceEndOffset;
    const sentence = contents.slice(sentenceStart, sentenceEnd === -1 ? contents.length : sentenceEnd);
    const phraseOffset = match.index - sentenceStart;
    const prefix = sentence.slice(0, phraseOffset);
    const suffix = sentence.slice(phraseOffset + match[0].length);
    if (/\b(?:no|not|never|without)\s*$/iu.test(prefix)) continue;
    if (
      negativeAvailabilityPredicate.test(suffix)
      || unnegatedProhibitionPredicate.test(suffix)
    ) continue;
    return true;
  }
  return false;
}
