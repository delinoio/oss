import { describe, expect, it } from "vitest";
import { canonicalDevHudSettings, decodeDevHudSettings, decodeVersionedDevHudSettings, defaultDevHudSettings, encodeDevHudSettings, parseDevHudSettings, PreviousSettingsSchemaVersion, SettingsContractError, SettingsSchemaVersion } from "./settings-contract";
import { diffSettings, redactRecursively, RedactedValue } from "./settings-diff";

describe("DevHud settings boundary", () => {
  it("migrates schema v1 to v3 with explicit unselected GitHub profiles", () => {
    const legacy = {
      ...defaultDevHudSettings,
      schemaVersion: 1,
      github: { repositories: [{ owner: "octo", name: "private" }], issueTracker: { owner: "octo", repository: "private", labels: ["bug"] } },
      decks: [],
    };
    const parsed = parseDevHudSettings(legacy);
    expect(parsed.schemaVersion).toBe(3);
    expect(parsed.github).toEqual({ profiles: [], pendingPatRemovals: [], repositories: [{ owner: "octo", name: "private", profileRef: null }], issueTracker: { owner: "octo", repository: "private", labels: ["bug"], profileRef: null } });
  });

  it("accepts non-secret GitHub profile descriptors and rejects secret fields or duplicate IDs", () => {
    const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
    const settings = { ...defaultDevHudSettings, github: { profiles: [profile], pendingPatRemovals: [], repositories: [{ owner: "octo", name: "private", profileRef: profile.id }], issueTracker: null } };
    expect(parseDevHudSettings(settings).github.profiles).toEqual([profile]);
    expect(canonicalDevHudSettings(settings)).not.toMatch(/token|secret|authorization/iu);
    expect(() => parseDevHudSettings({ ...settings, github: { ...settings.github, profiles: [profile, profile] } })).toThrow(/unique IDs/u);
    expect(() => parseDevHudSettings({ ...settings, github: { ...settings.github, profiles: [{ ...profile, token: "plain" }] } })).toThrow(/token/iu);
    expect(() => parseDevHudSettings({ ...settings, github: { ...settings.github, repositories: [{ owner: "octo", name: "private", profileRef: "missing" }] } })).toThrow(/configured GitHub profile/u);
  });

  it("accepts only disjoint canonical pending PAT removal tombstones", () => {
    const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
    const removedProfileId = "018f47a2-7b3c-7def-8abc-1234567890ac";
    expect(parseDevHudSettings({ ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, pendingPatRemovals: [removedProfileId] } }).github.pendingPatRemovals).toEqual([removedProfileId]);
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, profiles: [profile], pendingPatRemovals: [profile.id] } })).toThrow(/active GitHub profile/u);
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, pendingPatRemovals: [removedProfileId, removedProfileId] } })).toThrow(/unique IDs/u);
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, pendingPatRemovals: ["profile"] } })).toThrow(/UUID v7/u);
  });
  it("round trips the exact versioned non-secret contract canonically", () => {
    const encoded = encodeDevHudSettings(defaultDevHudSettings);
    expect(decodeDevHudSettings(encoded)).toEqual(defaultDevHudSettings);
    expect(decodeVersionedDevHudSettings(encoded, SettingsSchemaVersion)).toEqual(defaultDevHudSettings);
    expect(() => decodeVersionedDevHudSettings(encoded, 1)).toThrow(/snapshot envelope/u);
    expect(new TextDecoder().decode(encoded)).toBe(canonicalDevHudSettings(defaultDevHudSettings));
  });

  it.each(["token", "githubPat", "r2_secret_access_key", "apiUrl", "agentPath", "windowState", "permissions"])("rejects forbidden recursive field %s", (field) => {
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, [field]: "nope" } })).toThrow(SettingsContractError);
  });

  it("rejects unknown fields and embedded credential patterns", () => {
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, future: true })).toThrow(/unknown field/u);
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, repositories: [{ owner: "github_pat_secret", name: "oss" }] } })).toThrow(/secret material/u);
  });

  it.each(["__proto__", "constructor", "prototype"])("rejects prototype-sensitive shortcut action %s", (action) => {
    const shortcuts = JSON.parse(`{"${action}":"ControlRight+KeyK"}`) as Record<string, string>;
    expect(() => parseDevHudSettings({
      ...defaultDevHudSettings,
      shortcuts: { ...defaultDevHudSettings.shortcuts, desktop: shortcuts },
    })).toThrow(SettingsContractError);
  });

  it("rejects settings snapshots containing more than 25 Decks", () => {
    const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
    const deck = {
      id: "018f47a2-7b3c-7def-8abc-1234567890ac",
      name: "Deck",
      query: "repo:octo/widgets is:pr",
      builder: null,
      profileRef: profile.id,
      display: { groupBy: "none", showDrafts: true },
      refreshMinutes: 15,
      notifications: [],
    };
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, profiles: [profile] }, decks: Array.from({ length: 26 }, (_, index) => ({ ...deck, id: `018f47a2-7b3c-7def-8abc-${String(index).padStart(12, "0")}` })) })).toThrow(/at most 25/u);
  });

  it("accepts only canonical UUID-v7 Deck IDs", () => {
    const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
    const deck = {
      id: "018f47a2-7b3c-7def-8abc-1234567890ab",
      name: "Deck",
      query: "repo:octo/widgets is:pr",
      builder: null,
      profileRef: profile.id,
      display: { groupBy: "none", showDrafts: true },
      refreshMinutes: 15,
      notifications: [],
    };

    const settings = { ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, profiles: [profile] } };
    expect(parseDevHudSettings({ ...settings, decks: [deck] }).decks[0]?.id).toBe(deck.id);
    expect(() => parseDevHudSettings({ ...settings, decks: [{ ...deck, id: "deck" }] })).toThrow(/UUID v7/u);
    expect(() => parseDevHudSettings({ ...settings, decks: [{ ...deck, id: deck.id.toUpperCase() }] })).toThrow(/UUID v7/u);
  });

  it("requires an explicit configured GitHub profile for every Deck", () => {
    const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
    const deck = {
      id: "018f47a2-7b3c-7def-8abc-1234567890ac",
      name: "Deck",
      query: "repo:octo/private is:pr",
      builder: null,
      profileRef: profile.id,
      display: { groupBy: "none", showDrafts: true },
      refreshMinutes: 15,
      notifications: [],
    };
    const settings = { ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, profiles: [profile] }, decks: [deck] };
    expect(parseDevHudSettings(settings).decks[0]?.profileRef).toBe(profile.id);
    expect(() => parseDevHudSettings({ ...settings, decks: [{ ...deck, profileRef: "missing" }] })).toThrow(/configured GitHub profile/u);
    expect(() => parseDevHudSettings({ ...settings, decks: [{ ...deck, profileRef: null }] })).toThrow(/must select a local GitHub credential profile/u);
  });

  it("migrates v2 Deck repository scopes and duplicate notifications", () => {
    const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
    const legacy = {
      ...defaultDevHudSettings,
      schemaVersion: PreviousSettingsSchemaVersion,
      github: { ...defaultDevHudSettings.github, profiles: [profile] },
      decks: [{
        id: "018f47a2-7b3c-7def-8abc-1234567890ac",
        title: "Legacy Deck",
        query: "is:pr",
        repository: "octo/widgets",
        profileRef: profile.id,
        display: { groupBy: "none", showDrafts: true },
        refreshMinutes: 5,
        notifications: ["review", "review", "merged"],
      }],
    };
    expect(parseDevHudSettings(legacy).decks).toMatchObject([{ query: "is:pr repo:octo/widgets", builder: { repository: "octo/widgets" }, notifications: ["review", "merged"] }]);
  });

  it("retains a v2 Deck repository scope when its query names another repository", () => {
    const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
    const legacy = {
      ...defaultDevHudSettings,
      schemaVersion: PreviousSettingsSchemaVersion,
      github: { ...defaultDevHudSettings.github, profiles: [profile] },
      decks: [{
        id: "018f47a2-7b3c-7def-8abc-1234567890ac",
        title: "Legacy Deck",
        query: "repo:octo/other is:pr",
        repository: "octo/widgets",
        profileRef: profile.id,
        display: { groupBy: "none", showDrafts: true },
        refreshMinutes: 5,
        notifications: [],
      }],
    };
    const migrated = parseDevHudSettings(legacy);
    expect(migrated.decks[0]?.query).toBe("repo:octo/other is:pr repo:octo/widgets");
    expect(migrated.decks[0]?.builder).toMatchObject({ repository: "octo/other" });
    expect(() => encodeDevHudSettings(migrated)).not.toThrow();
    expect(parseDevHudSettings({ ...legacy, decks: [{ ...legacy.decks[0], repository: "octo/other" }] }).decks[0]?.query).toBe("repo:octo/other is:pr");
    expect(() => parseDevHudSettings({ ...legacy, decks: [{ ...legacy.decks[0], repository: null }] })).toThrow(/repository.*selected/u);
  });

  it("requires real repository-scoped pull-request qualifiers in v3 Decks", () => {
    const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
    const deck = {
      id: "018f47a2-7b3c-7def-8abc-1234567890ac",
      name: "Deck",
      query: "repo:octo/widgets IS:PR",
      builder: null,
      profileRef: profile.id,
      display: { groupBy: "none", showDrafts: true },
      refreshMinutes: 5,
      notifications: [],
    };
    const settings = { ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, profiles: [profile] }, decks: [deck] };
    expect(parseDevHudSettings(settings).decks).toHaveLength(1);
    expect(() => parseDevHudSettings({ ...settings, decks: [{ ...deck, query: "is:pr" }] })).toThrow(/repository qualifier/u);
    expect(() => parseDevHudSettings({ ...settings, decks: [{ ...deck, query: "repo:octo is:pr" }] })).toThrow(/repository qualifier/u);
    expect(() => parseDevHudSettings({ ...settings, decks: [{ ...deck, query: "repo:octo/\u0000 is:pr" }] })).toThrow(/repository qualifier/u);
    expect(() => parseDevHudSettings({ ...settings, decks: [{ ...deck, query: `${Array.from({ length: 11 }, (_, index) => `repo:octo/repository-${index}`).join(" ")} is:pr` }] })).toThrow(/repository qualifier/u);
    expect(() => parseDevHudSettings({ ...settings, decks: [{ ...deck, name: "   " }] })).toThrow(/trimmed nonblank/u);
    expect(() => parseDevHudSettings({ ...settings, decks: [{ ...deck, name: " Deck " }] })).toThrow(/trimmed nonblank/u);
    expect(parseDevHudSettings({ ...settings, decks: [{ ...deck, query: '"find is:pr here" repo:octo/widgets' }] }).decks[0]?.query).toBe('"find is:pr here" repo:octo/widgets is:pr');
    expect(() => parseDevHudSettings({ ...settings, decks: [{ ...deck, notifications: ["review", "review"] }] })).toThrow(/unique values/u);
    expect(() => parseDevHudSettings({ ...settings, decks: [deck, deck] })).toThrow(/unique IDs/u);
  });

  it("requires a non-null Deck builder to match the executable query", () => {
    const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
    const deck = { id: "018f47a2-7b3c-7def-8abc-1234567890ac", name: "Deck", profileRef: profile.id, query: "repo:octo/widgets is:pr", builder: { repository: "octo/widgets", author: null, review: null, label: null, state: null }, display: { groupBy: "none" as const, showDrafts: true }, refreshMinutes: 5 as const, notifications: [] };
    const settings = { ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, profiles: [profile] }, decks: [deck] };
    expect(parseDevHudSettings(settings).decks).toHaveLength(1);
    expect(() => parseDevHudSettings({ ...settings, decks: [{ ...deck, builder: { ...deck.builder, repository: "other/repository" } }] })).toThrow(/lossless projection/u);
  });

  it.each(["?token=plain-secret", "?X-Amz-Signature=plain-secret", "#credential", "?", "#"])("rejects query or fragment delimiters in synchronized URL fields: %s", (suffix) => {
    expect(() => parseDevHudSettings({
      ...defaultDevHudSettings,
      urlMappings: [{ sourcePrefix: `https://source.example/path${suffix}`, destinationPrefix: "https://destination.example/path" }],
    })).toThrow(/without credentials, query, or fragment/u);
    expect(() => parseDevHudSettings({
      ...defaultDevHudSettings,
      urlMappings: [{ sourcePrefix: "https://source.example/path", destinationPrefix: `https://destination.example/path${suffix}` }],
    })).toThrow(/without credentials, query, or fragment/u);
    expect(() => parseDevHudSettings({
      ...defaultDevHudSettings,
      uploads: {
        provider: "r2",
        r2: { profileRef: "profile", bucket: "bucket", endpoint: `https://r2.example${suffix}`, region: "auto", publicBaseUrl: null },
      },
    })).toThrow(/without credentials, query, or fragment/u);
    expect(() => parseDevHudSettings({
      ...defaultDevHudSettings,
      uploads: {
        provider: "r2",
        r2: { profileRef: "profile", bucket: "bucket", endpoint: "https://r2.example", region: "auto", publicBaseUrl: `https://cdn.example${suffix}` },
      },
    })).toThrow(/without credentials, query, or fragment/u);
  });

  it("produces a complete recursive, secret-redacted snapshot diff", () => {
    const local = { deck: { title: "Local", nested: [1, { token: "cleartext" }] }, extra: true };
    const server = { deck: { title: "Server", nested: [2, { token: "different" }] }, added: "github_pat_abcdefghijklmnopqrstuvwxyz" };
    expect(diffSettings(local, server)).toEqual([
      { path: "$.added", kind: "added", local: undefined, server: RedactedValue },
      { path: "$.deck.nested[0]", kind: "changed", local: 1, server: 2 },
      { path: "$.deck.title", kind: "changed", local: "Local", server: "Server" },
      { path: "$.extra", kind: "removed", local: true, server: undefined },
    ]);
    expect(redactRecursively({ a: [{ secret: "value" }] })).toEqual({ a: [{ secret: RedactedValue }] });
  });
});
