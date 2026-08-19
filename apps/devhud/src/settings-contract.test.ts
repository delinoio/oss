import { describe, expect, it } from "vitest";
import { canonicalDevHudSettings, decodeDevHudSettings, decodeVersionedDevHudSettings, defaultDevHudSettings, encodeDevHudSettings, MaximumUrlRepositoryMappings, parseDevHudSettings, SettingsContractError, SettingsSchemaVersion } from "./settings-contract";
import { diffSettings, redactRecursively, RedactedValue } from "./settings-diff";
import { ShortcutActionId, ShortcutKey, ShortcutModifier, ShortcutValidationCode, defaultDesktopShortcutBindings, parseDesktopShortcutBindings } from "./shortcuts";

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

  it("uses portable structured right-primary defaults and upgrades an empty legacy desktop map", () => {
    expect(defaultDesktopShortcutBindings[ShortcutActionId.CommandPalette]).toEqual({ enabled: true, modifiers: [ShortcutModifier.RightPrimary], key: ShortcutKey.K });
    expect(defaultDesktopShortcutBindings[ShortcutActionId.CaptureToolbar]).toEqual({ enabled: true, modifiers: [], key: ShortcutKey.Digit5 });
    expect(parseDesktopShortcutBindings({})).toEqual(defaultDesktopShortcutBindings);
    expect(parseDevHudSettings({ ...defaultDevHudSettings, schemaVersion: 1, github: { repositories: [], issueTracker: null }, shortcuts: { desktop: {}, ios: {}, android: {} } }).shortcuts.desktop).toEqual(defaultDesktopShortcutBindings);
  });

  it("upgrades every previously accepted v1 shortcut map without retaining raw chords", () => {
    const parsed = parseDevHudSettings({
      ...defaultDevHudSettings,
      schemaVersion: 1,
      github: { repositories: [], issueTracker: null },
      shortcuts: {
        desktop: { "legacy.palette": "ControlRight+KeyK" },
        ios: { "legacy.ios": "MetaRight+KeyK" },
        android: { "legacy.android": "ControlRight+KeyK" },
      },
    });
    expect(parsed.shortcuts.desktop).toEqual(defaultDesktopShortcutBindings);
    expect(parsed.shortcuts.ios).toEqual({});
    expect(parsed.shortcuts.android).toEqual({});
    expect(canonicalDevHudSettings(parsed)).not.toContain("ControlRight");
  });

  it("rejects malformed and conflicting shortcut chords before settings persistence", () => {
    const duplicate = structuredShortcuts();
    duplicate[ShortcutActionId.CaptureDisplay] = { ...duplicate[ShortcutActionId.CaptureDisplay], key: ShortcutKey.Digit2 };
    expect(() => parseDesktopShortcutBindings(duplicate)).toThrow(ShortcutValidationCode.Conflict);

    const reorderedModifiers = structuredShortcuts();
    reorderedModifiers[ShortcutActionId.CommandPalette] = { enabled: true, modifiers: [ShortcutModifier.Shift, ShortcutModifier.Alt], key: ShortcutKey.K };
    reorderedModifiers[ShortcutActionId.CaptureDisplay] = { enabled: true, modifiers: [ShortcutModifier.Alt, ShortcutModifier.Shift], key: ShortcutKey.K };
    expect(() => parseDesktopShortcutBindings(reorderedModifiers)).toThrow(ShortcutValidationCode.Conflict);

    const reserved = structuredShortcuts();
    reserved[ShortcutActionId.CommandPalette] = { enabled: true, modifiers: [ShortcutModifier.RightPrimary], key: ShortcutKey.Space };
    expect(parseDesktopShortcutBindings(reserved)).toEqual(reserved);

    const malformed = structuredShortcuts();
    malformed[ShortcutActionId.CommandPalette] = { enabled: true, modifiers: [], key: ShortcutKey.K };
    expect(() => parseDesktopShortcutBindings(malformed)).toThrow(ShortcutValidationCode.Malformed);
  });

  it("accepts explicit disabling while persisting only modifier and key enums", () => {
    const disabled = structuredShortcuts();
    disabled[ShortcutActionId.CaptureSelection] = { ...disabled[ShortcutActionId.CaptureSelection], enabled: false };
    const parsed = parseDesktopShortcutBindings(disabled);
    expect(parsed[ShortcutActionId.CaptureSelection].enabled).toBe(false);
    expect(canonicalDevHudSettings({ ...defaultDevHudSettings, shortcuts: { desktop: parsed, ios: {}, android: {} } })).toContain('"right-primary"');
    expect(canonicalDevHudSettings({ ...defaultDevHudSettings, shortcuts: { desktop: parsed, ios: {}, android: {} } })).not.toContain("ControlRight");
  });

  it("rejects settings snapshots containing more than 25 Decks", () => {
    const deck = {
      id: "deck",
      title: "Deck",
      query: "is:pr",
      repository: null,
      profileRef: null,
      display: { groupBy: "none", showDrafts: true },
      refreshMinutes: 15,
      notifications: [],
    };
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, decks: Array.from({ length: 26 }, (_, index) => ({ ...deck, id: `deck-${index}` })) })).toThrow(/at most 25/u);
  });

  it("accepts only canonical UUID-v7 Deck IDs", () => {
    const deck = {
      id: "018f47a2-7b3c-7def-8abc-1234567890ab",
      title: "Deck",
      query: "is:pr",
      repository: null,
      profileRef: null,
      display: { groupBy: "none", showDrafts: true },
      refreshMinutes: 15,
      notifications: [],
    };

    expect(parseDevHudSettings({ ...defaultDevHudSettings, decks: [deck] }).decks[0]?.id).toBe(deck.id);
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, decks: [{ ...deck, id: "deck" }] })).toThrow(/UUID v7/u);
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, decks: [{ ...deck, id: deck.id.toUpperCase() }] })).toThrow(/UUID v7/u);
  });

  it("rejects a GitHub profile reference when a Deck has no repository", () => {
    const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
    const deck = {
      id: "018f47a2-7b3c-7def-8abc-1234567890ac",
      title: "Deck",
      query: "is:pr",
      repository: null,
      profileRef: profile.id,
      display: { groupBy: "none", showDrafts: true },
      refreshMinutes: 15,
      notifications: [],
    };
    const settings = { ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, profiles: [profile] }, decks: [deck] };
    expect(() => parseDevHudSettings(settings)).toThrow(/profileRef.*repository is null/u);
    expect(parseDevHudSettings({ ...settings, decks: [{ ...deck, profileRef: null }] }).decks[0]?.profileRef).toBeNull();
  });

  const mappingProfile = { id: "018f47a2-7b3c-7def-8abc-1234567890ac", name: "Work", kind: "fine-grained" as const };
  const mapping = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", pattern: "https://source.example/path", repository: { owner: "delinoio", name: "oss" }, credentialProfileRef: mappingProfile.id, priority: 0, chromeOrigin: null, updatedAt: "2026-08-17T00:00:00.000Z" };
  const settingsWithMappingProfile = { ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, profiles: [mappingProfile] } };

  it.each(["?token=plain-secret", "?X-Amz-Signature=plain-secret", "#credential", "?", "#"])("rejects query or fragment delimiters in synchronized URL fields: %s", (suffix) => {
    expect(() => parseDevHudSettings({
      ...settingsWithMappingProfile,
      urlMappings: [{ ...mapping, pattern: `https://source.example/path${suffix}` }],
    })).toThrow(/credentials, query, or fragment/u);
    expect(() => parseDevHudSettings({
      ...settingsWithMappingProfile,
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

  it.each(["https://source.example/", "https://SOURCE.example", "https://source.example:443"])("normalizes equivalent Chrome origins: %s", (chromeOrigin) => {
    expect(parseDevHudSettings({ ...settingsWithMappingProfile, urlMappings: [{ ...mapping, chromeOrigin }] }).urlMappings[0]?.chromeOrigin).toBe("https://source.example");
  });

  it("requires a Chrome origin to be covered by the mapping authority", () => {
    for (const [pattern, chromeOrigin] of [
      ["http://source.example/**", "https://source.example"],
      ["https://source.example/**", "https://other.example"],
      ["https://source.example:8443/**", "https://source.example:9443"],
    ]) {
      expect(() => parseDevHudSettings({ ...settingsWithMappingProfile, urlMappings: [{ ...mapping, pattern, chromeOrigin }] })).toThrow(/chromeOrigin.*scheme, host, and port/u);
    }
    expect(parseDevHudSettings({
      ...settingsWithMappingProfile,
      urlMappings: [{ ...mapping, pattern: "*://*.example:*", chromeOrigin: "https://source.example:8443" }],
    }).urlMappings[0]?.chromeOrigin).toBe("https://source.example:8443");
  });

  it("rejects wildcard Chrome origins and excessive mapping counts", () => {
    expect(() => parseDevHudSettings({ ...settingsWithMappingProfile, urlMappings: [{ ...mapping, chromeOrigin: "https://*.example.com" }] })).toThrow(/concrete HTTP\(S\) origin/u);
    expect(() => parseDevHudSettings({ ...settingsWithMappingProfile, urlMappings: Array.from({ length: MaximumUrlRepositoryMappings + 1 }, (_, index) => ({ ...mapping, id: `018f47a2-7b3c-7def-8abc-${(123456789000 + index).toString().padStart(12, "0")}` })) })).toThrow(/at most/u);
  });

  it("requires each URL mapping to reference a configured GitHub profile", () => {
    expect(() => parseDevHudSettings({ ...settingsWithMappingProfile, urlMappings: [{ ...mapping, credentialProfileRef: "missing" }] })).toThrow(/urlMappings\[0\].credentialProfileRef.*configured GitHub profile/u);
  });

  it("drops legacy v1 mapping entries while preserving other settings", () => {
    const legacy = { ...defaultDevHudSettings, schemaVersion: 1, appearance: { theme: "dark", language: "ko" }, github: { repositories: [], issueTracker: null }, urlMappings: [{ sourcePrefix: "https://source.example/path", destinationPrefix: "https://destination.example/path" }] };
    expect(parseDevHudSettings(legacy)).toMatchObject({ schemaVersion: 3, appearance: { theme: "dark", language: "ko" }, urlMappings: [] });
  });

  it("drops prefix mapping entries from schema-v2 snapshots", () => {
    const legacy = { ...settingsWithMappingProfile, schemaVersion: 2, urlMappings: [{ sourcePrefix: "https://source.example/path", destinationPrefix: "https://destination.example/path" }] };
    expect(parseDevHudSettings(legacy).urlMappings).toEqual([]);
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

function structuredShortcuts(): Record<typeof ShortcutActionId[keyof typeof ShortcutActionId], { enabled: boolean; modifiers: readonly (typeof ShortcutModifier)[keyof typeof ShortcutModifier][]; key: (typeof ShortcutKey)[keyof typeof ShortcutKey] }> {
  return Object.fromEntries(Object.entries(defaultDesktopShortcutBindings).map(([action, binding]) => [action, { ...binding, modifiers: [...binding.modifiers] }])) as unknown as Record<typeof ShortcutActionId[keyof typeof ShortcutActionId], { enabled: boolean; modifiers: readonly (typeof ShortcutModifier)[keyof typeof ShortcutModifier][]; key: (typeof ShortcutKey)[keyof typeof ShortcutKey] }>;
}
