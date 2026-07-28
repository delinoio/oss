import { describe, expect, it } from "vitest";

import { ShortcutKey, ShortcutModifier, type ShortcutDefinition } from "../persistence/contracts";
import { planShortcutDefinitions } from "./shortcutRegistry";

const shortcut = (key: ShortcutKey) => ({ modifiers: [ShortcutModifier.Control], key });
const deck = (accountId: string, definitionId: string, key: ShortcutKey): ShortcutDefinition => ({
  owner: { feature: "deck", accountId, definitionId: definitionId as never }, shortcut: shortcut(key),
});
const realQa = (definitionId: string, key: ShortcutKey): ShortcutDefinition => ({
  owner: { feature: "realqa", definitionId: definitionId as never }, shortcut: shortcut(key),
});

describe("planShortcutDefinitions", () => {
  it("resolves conflicts independently of concurrent arrival order", () => {
    const first = deck("account-a", "z", ShortcutKey.K);
    const second = deck("account-a", "a", ShortcutKey.K);
    const outcomes = planShortcutDefinitions([first, second], true);
    expect(outcomes.get("deck:account-a:a")).toEqual({ status: "active" });
    expect(outcomes.get("deck:account-a:z")).toEqual({ status: "inactive", reason: "conflict" });
  });

  it("enforces per-account Deck and per-device RealQA limits without crossing accounts", () => {
    const definitions: ShortcutDefinition[] = Array.from({ length: 21 }, (_, index) =>
      deck("account-a", `a-${index}`, ShortcutKey.F1),
    ).map((definition, index) => ({
      ...definition,
      shortcut: {
        modifiers: [index < 12 ? ShortcutModifier.Control : ShortcutModifier.Shift],
        key: (`f${(index % 12) + 1}`) as ShortcutKey,
      },
    }));
    definitions.push(deck("account-b", "b", ShortcutKey.K));
    const outcomes = planShortcutDefinitions(definitions, true);
    expect([...outcomes.values()].filter((outcome) => outcome.status === "inactive" && outcome.reason === "limit-exceeded")).toHaveLength(1);
    expect(outcomes.get("deck:account-b:b")).toEqual({ status: "active" });
  });

  it("counts only accepted Deck and RealQA definitions toward feature limits", () => {
    const definitions: ShortcutDefinition[] = Array.from({ length: 20 }, (_, index) =>
      deck("account-a", `conflict-${index}`, ShortcutKey.K),
    );
    definitions.push(deck("account-a", "z-accepted", ShortcutKey.P));
    definitions.push(...Array.from({ length: 20 }, (_, index) =>
      realQa(`conflict-${index}`, ShortcutKey.K),
    ));
    definitions.push(realQa("z-accepted", ShortcutKey.F2));

    const outcomes = planShortcutDefinitions(definitions, true);

    expect(outcomes.get("deck:account-a:z-accepted")).toEqual({ status: "active" });
    expect(outcomes.get("realqa:z-accepted")).toEqual({ status: "active" });
  });

  it("rejects malformed definitions before planning them active", () => {
    const malformed = {
      owner: { feature: "deck", accountId: "account-a", definitionId: "malformed" as never },
      shortcut: { modifiers: [], key: ShortcutKey.K },
    } as ShortcutDefinition;

    const outcomes = planShortcutDefinitions([malformed], true);

    expect(outcomes.get("deck:account-a:malformed")).toEqual({ status: "inactive", reason: "malformed" });
  });

  it("deduplicates repeated owners before checking binding conflicts", () => {
    const repeated = deck("account-a", "view", ShortcutKey.K);

    const outcomes = planShortcutDefinitions([repeated, repeated], true);

    expect(outcomes.get("deck:account-a:view")).toEqual({ status: "active" });
  });

  it("keeps unavailable feature definitions inactive and leaves the generic binding alone", () => {
    const outcomes = planShortcutDefinitions([
      { owner: { feature: "devhud" }, shortcut: shortcut(ShortcutKey.K) },
      deck("account-a", "view", ShortcutKey.P),
    ], false);
    expect(outcomes.get("devhud")).toEqual({ status: "active" });
    expect(outcomes.get("deck:account-a:view")).toEqual({ status: "inactive", reason: "unavailable" });
  });

  it("gives the generic DevHud binding priority over feature definitions", () => {
    const outcomes = planShortcutDefinitions([
      deck("account-a", "view", ShortcutKey.K),
      { owner: { feature: "devhud" }, shortcut: shortcut(ShortcutKey.K) },
    ], true);
    expect(outcomes.get("devhud")).toEqual({ status: "active" });
    expect(outcomes.get("deck:account-a:view")).toEqual({ status: "inactive", reason: "conflict" });
  });
});
