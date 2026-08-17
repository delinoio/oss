import { describe, expect, it } from "vitest";
import { canonicalDevHudSettings, decodeDevHudSettings, defaultDevHudSettings, encodeDevHudSettings, parseDevHudSettings, SettingsContractError } from "./settings-contract";
import { diffSettings, redactRecursively, RedactedValue } from "./settings-diff";
import { ShortcutActionId, ShortcutKey, ShortcutModifier, ShortcutValidationCode, defaultDesktopShortcutBindings, parseDesktopShortcutBindings } from "./shortcuts";

describe("DevHud settings boundary", () => {
  it("round trips the exact versioned non-secret contract canonically", () => {
    const encoded = encodeDevHudSettings(defaultDevHudSettings);
    expect(decodeDevHudSettings(encoded)).toEqual(defaultDevHudSettings);
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
    expect(parseDevHudSettings({ ...defaultDevHudSettings, shortcuts: { desktop: {}, ios: {}, android: {} } }).shortcuts.desktop).toEqual(defaultDesktopShortcutBindings);
  });

  it("upgrades every previously accepted v1 shortcut map without retaining raw chords", () => {
    const parsed = parseDevHudSettings({
      ...defaultDevHudSettings,
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
      display: { groupBy: "none", showDrafts: true },
      refreshMinutes: 15,
      notifications: [],
    };

    expect(parseDevHudSettings({ ...defaultDevHudSettings, decks: [deck] }).decks[0]?.id).toBe(deck.id);
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, decks: [{ ...deck, id: "deck" }] })).toThrow(/UUID v7/u);
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, decks: [{ ...deck, id: deck.id.toUpperCase() }] })).toThrow(/UUID v7/u);
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

function structuredShortcuts(): Record<typeof ShortcutActionId[keyof typeof ShortcutActionId], { enabled: boolean; modifiers: readonly (typeof ShortcutModifier)[keyof typeof ShortcutModifier][]; key: (typeof ShortcutKey)[keyof typeof ShortcutKey] }> {
  return Object.fromEntries(Object.entries(defaultDesktopShortcutBindings).map(([action, binding]) => [action, { ...binding, modifiers: [...binding.modifiers] }])) as unknown as Record<typeof ShortcutActionId[keyof typeof ShortcutActionId], { enabled: boolean; modifiers: readonly (typeof ShortcutModifier)[keyof typeof ShortcutModifier][]; key: (typeof ShortcutKey)[keyof typeof ShortcutKey] }>;
}
