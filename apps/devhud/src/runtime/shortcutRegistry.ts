import {
  MAX_DECK_SHORTCUT_DEFINITIONS,
  MAX_REALQA_SHORTCUT_DEFINITIONS,
  type ShortcutDefinition,
  type ShortcutOwner,
  type StructuredShortcut,
} from "../persistence/contracts";

export type ShortcutDefinitionOutcome =
  | { readonly status: "active" }
  | { readonly status: "inactive"; readonly reason: "conflict" | "unavailable" | "limit-exceeded" };

function bindingKey(shortcut: StructuredShortcut): string {
  return `${[...shortcut.modifiers].sort().join("+")}:${shortcut.key}`;
}

function ownerKey(owner: ShortcutOwner): string {
  switch (owner.feature) {
    case "devhud": return "devhud";
    case "deck": return `deck:${owner.accountId}:${owner.definitionId}`;
    case "realqa": return `realqa:${owner.definitionId}`;
  }
}

/**
 * A deterministic, pure planning step shared by desktop UI flows. Native code
 * remains the authority for actual registration and returns the final local
 * outcome. Definitions are ordered by stable owner identity, never arrival
 * timing, so reconnecting a feature cannot steal a binding from another one.
 */
export function planShortcutDefinitions(
  definitions: readonly ShortcutDefinition[],
  available: boolean,
): ReadonlyMap<string, ShortcutDefinitionOutcome> {
  const outcomes = new Map<string, ShortcutDefinitionOutcome>();
  const deckCounts = new Map<string, number>();
  let realQaCount = 0;
  const accepted: ShortcutDefinition[] = [];

  for (const definition of [...definitions].sort((left, right) =>
    ownerKey(left.owner).localeCompare(ownerKey(right.owner)),
  )) {
    const key = ownerKey(definition.owner);
    if (!available && definition.owner.feature !== "devhud") {
      outcomes.set(key, { status: "inactive", reason: "unavailable" });
      continue;
    }
    if (definition.owner.feature === "deck") {
      const count = deckCounts.get(definition.owner.accountId) ?? 0;
      if (count >= MAX_DECK_SHORTCUT_DEFINITIONS) {
        outcomes.set(key, { status: "inactive", reason: "limit-exceeded" });
        continue;
      }
      deckCounts.set(definition.owner.accountId, count + 1);
    }
    if (definition.owner.feature === "realqa") {
      if (realQaCount >= MAX_REALQA_SHORTCUT_DEFINITIONS) {
        outcomes.set(key, { status: "inactive", reason: "limit-exceeded" });
        continue;
      }
      realQaCount += 1;
    }
    const duplicate = accepted.some((entry) => bindingKey(entry.shortcut) === bindingKey(definition.shortcut));
    if (duplicate) {
      outcomes.set(key, { status: "inactive", reason: "conflict" });
      continue;
    }
    accepted.push(definition);
    outcomes.set(key, { status: "active" });
  }
  return outcomes;
}

export { bindingKey, ownerKey };
