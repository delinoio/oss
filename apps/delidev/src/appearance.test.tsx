// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useState } from "react";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import {
  AppearanceProblem,
  AppearanceProvider,
  AppearanceSettings,
  Theme,
  parseAppearance,
  useAppearanceReady,
  type AppearanceBridge,
  type AppearanceSnapshot,
} from "./appearance";
import { danger, expectTone, neutral } from "./test-action-tone";

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
function fixture(theme = Theme.System) {
  let snapshot: AppearanceSnapshot = { revision: 1, theme, problem: null };
  const listeners = new Set<(snapshot: unknown) => void>();
  const bridge: AppearanceBridge = {
    read: vi.fn(async () => snapshot),
    update: vi.fn(async (theme, revision) => {
      if (revision !== snapshot.revision)
        return { ...snapshot, problem: AppearanceProblem.Changed };
      snapshot = { revision: revision + 1, theme, problem: null };
      listeners.forEach((changed) => changed(snapshot));
      return snapshot;
    }),
    subscribe: vi.fn(async (changed) => {
      listeners.add(changed);
      return () => {
        listeners.delete(changed);
      };
    }),
  };
  return {
    bridge,
    listeners,
    publish: (value: AppearanceSnapshot) => {
      snapshot = value;
      listeners.forEach((changed) => changed(value));
    },
  };
}
function scheme(dark: boolean) {
  const listeners = new Set<() => void>();
  const media = {
    matches: dark,
    addEventListener: (_: string, listener: () => void) =>
      listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) =>
      listeners.delete(listener),
  };
  vi.stubGlobal("matchMedia", () => media);
  return (next: boolean) =>
    act(() => {
      media.matches = next;
      listeners.forEach((changed) => changed());
    });
}
afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.theme;
});

test("System follows live OS changes, explicit choices ignore them and drafts survive", async () => {
  const changeScheme = scheme(true),
    value = fixture();
  render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
      <textarea aria-label="Composer draft" defaultValue="unsent" />
    </AppearanceProvider>,
  );
  await waitFor(() =>
    expect(
      (
        screen.getByRole("radio", { name: "System" }) as HTMLInputElement
      ).matches(":disabled"),
    ).toBe(false),
  );
  expect(document.documentElement.dataset.theme).toBe(Theme.Dark);
  changeScheme(false);
  expect(document.documentElement.dataset.theme).toBe(Theme.Light);
  fireEvent.click(screen.getByRole("radio", { name: "Dark" }));
  await waitFor(() =>
    expect(
      (screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).checked,
    ).toBe(true),
  );
  changeScheme(true);
  changeScheme(false);
  expect(document.documentElement.dataset.theme).toBe(Theme.Dark);
  expect(
    (
      screen.getByRole("textbox", {
        name: "Composer draft",
      }) as HTMLTextAreaElement
    ).value,
  ).toBe("unsent");
  expect(value.bridge.update).toHaveBeenCalledWith(
    Theme.Dark,
    1,
    expect.any(Object),
  );
});

test("a delayed initial read and old events cannot replace a newer commit", async () => {
  scheme(false);
  const value = fixture(),
    initial = deferred<unknown>();
  vi.mocked(value.bridge.read).mockReturnValueOnce(initial.promise);
  render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  await waitFor(() => expect(value.bridge.read).toHaveBeenCalled());
  act(() => value.publish({ revision: 7, theme: Theme.Dark, problem: null }));
  await act(async () =>
    initial.resolve({ revision: 1, theme: Theme.Light, problem: null }),
  );
  act(() => value.publish({ revision: 3, theme: Theme.Light, problem: null }));
  expect(document.documentElement.dataset.theme).toBe(Theme.Dark);
  expect(
    (screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).checked,
  ).toBe(true);
});

test("failure retains the committed choice, exposes pending state and requires inspection", async () => {
  scheme(false);
  const value = fixture(Theme.Dark),
    update = deferred<unknown>();
  vi.mocked(value.bridge.update).mockReturnValueOnce(update.promise);
  render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  await waitFor(() =>
    expect(
      (
        screen.getByRole("radio", { name: "Light" }) as HTMLInputElement
      ).matches(":disabled"),
    ).toBe(false),
  );
  fireEvent.click(screen.getByRole("radio", { name: "Light" }));
  expect(screen.getByText("Saving theme…")).toBeTruthy();
  expect(document.documentElement.dataset.theme).toBe(Theme.Dark);
  await act(async () =>
    update.reject(new Error("private diagnostic must not render")),
  );
  expect(screen.queryByText(/private diagnostic/)).toBeNull();
  expect(screen.getAllByText(/save outcome is unknown/).length).toBeGreaterThan(
    0,
  );
  expect(
    (screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).checked,
  ).toBe(true);
  expect(
    (screen.getByRole("radio", { name: "Light" }) as HTMLInputElement).matches(
      ":disabled",
    ),
  ).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Reload appearance" }));
  await waitFor(() =>
    expect(
      (
        screen.getByRole("radio", { name: "Light" }) as HTMLInputElement
      ).matches(":disabled"),
    ).toBe(false),
  );
  fireEvent.click(screen.getByRole("radio", { name: "Light" }));
  await waitFor(() =>
    expect(document.documentElement.dataset.theme).toBe(Theme.Light),
  );
});

test("committed events update every live controller and new windows read that choice", async () => {
  scheme(false);
  const value = fixture();
  const first = render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  const second = render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  await waitFor(() =>
    expect(
      (
        within(first.container).getByRole("radio", {
          name: "Light",
        }) as HTMLInputElement
      ).matches(":disabled"),
    ).toBe(false),
  );
  await waitFor(() =>
    expect(
      (
        within(second.container).getByRole("radio", {
          name: "Light",
        }) as HTMLInputElement
      ).matches(":disabled"),
    ).toBe(false),
  );
  fireEvent.click(
    within(first.container).getByRole("radio", { name: "Light" }),
  );
  await waitFor(() =>
    expect(
      (
        within(second.container).getByRole("radio", {
          name: "Light",
        }) as HTMLInputElement
      ).checked,
    ).toBe(true),
  );
  first.unmount();
  second.unmount();
  render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  await waitFor(() =>
    expect(
      (screen.getByRole("radio", { name: "Light" }) as HTMLInputElement)
        .checked,
    ).toBe(true),
  );
});

test("closing Settings during a save keeps the device controller alive", async () => {
  scheme(false);
  const value = fixture(),
    update = deferred<unknown>();
  vi.mocked(value.bridge.update).mockReturnValueOnce(update.promise);
  function Opening() {
    const [open, setOpen] = useState(true);
    return (
      <>
        <button onClick={() => setOpen(!open)}>Toggle Settings</button>
        {open ? <AppearanceSettings /> : null}
      </>
    );
  }
  render(
    <StrictMode>
      <AppearanceProvider bridge={value.bridge}>
        <Opening />
      </AppearanceProvider>
    </StrictMode>,
  );
  await waitFor(() =>
    expect(
      (screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).matches(
        ":disabled",
      ),
    ).toBe(false),
  );
  fireEvent.click(screen.getByRole("radio", { name: "Dark" }));
  fireEvent.click(screen.getByText("Toggle Settings"));
  await act(async () =>
    update.resolve({ revision: 2, theme: Theme.Dark, problem: null }),
  );
  fireEvent.click(screen.getByText("Toggle Settings"));
  expect(
    (screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).checked,
  ).toBe(true);
  expect(value.listeners.size).toBe(1);
});

test("invalid or newer storage stays visible with System fallback and disabled writes", async () => {
  scheme(true);
  const value = fixture();
  vi.mocked(value.bridge.read).mockResolvedValue({
    revision: 1,
    theme: Theme.System,
    problem: AppearanceProblem.UnsupportedVersion,
  });
  render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  await screen.findAllByText(/unsupported version/);
  expect(document.documentElement.dataset.theme).toBe(Theme.Dark);
  expect(
    (screen.getByRole("radio", { name: "Light" }) as HTMLInputElement).matches(
      ":disabled",
    ),
  ).toBe(true);
  expect(value.bridge.update).not.toHaveBeenCalled();
});

test("a failed older read cannot hide a newer committed event", async () => {
  scheme(false);
  const value = fixture(),
    initial = deferred<unknown>();
  vi.mocked(value.bridge.read).mockReturnValueOnce(initial.promise);
  render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  await waitFor(() => expect(value.bridge.read).toHaveBeenCalled());
  act(() => value.publish({ revision: 9, theme: Theme.Dark, problem: null }));
  await act(async () => initial.reject(new Error("failed delayed read")));
  expect(
    screen.queryByRole("button", { name: "Reload appearance" }),
  ).toBeNull();
  expect(document.documentElement.dataset.theme).toBe(Theme.Dark);
});

test("validates the complete native snapshot before adoption", () => {
  for (const value of [
    null,
    {},
    { revision: -1, theme: Theme.Dark, problem: null },
    { revision: 1.5, theme: Theme.Dark, problem: null },
    { revision: 1, theme: "other", problem: null },
    { revision: 1, theme: Theme.Dark, problem: "private" },
    { revision: 1, theme: Theme.Dark, problem: null, extra: true },
  ])
    expect(() => parseAppearance(value)).toThrow();
});

test("reload restores a failed event subscription before enabling writes", async () => {
  scheme(false);
  const value = fixture();
  vi.mocked(value.bridge.subscribe).mockRejectedValueOnce(
    new Error("event subscription unavailable"),
  );
  render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  const reload = await screen.findByRole("button", {
    name: "Reload appearance",
  });
  // The initial fallback also renders Reload while subscription is pending.
  // Retry only after that first failed attempt has released its operation guard.
  await waitFor(() =>
    expect((reload as HTMLButtonElement).disabled).toBe(false),
  );
  expect(value.bridge.read).not.toHaveBeenCalled();
  expect(
    (screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).matches(
      ":disabled",
    ),
  ).toBe(true);
  fireEvent.click(reload);
  await waitFor(() =>
    expect(
      (screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).matches(
        ":disabled",
      ),
    ).toBe(false),
  );
  expect(value.listeners.size).toBe(1);
  act(() => value.publish({ revision: 2, theme: Theme.Dark, problem: null }));
  expect(
    (screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).checked,
  ).toBe(true);
});

test("foreground inspection reconciles a missed event without clearing uncertainty", async () => {
  scheme(false);
  const value = fixture();
  render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  await waitFor(() =>
    expect(
      (screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).matches(
        ":disabled",
      ),
    ).toBe(false),
  );
  vi.mocked(value.bridge.read).mockResolvedValue({
    revision: 3,
    theme: Theme.Dark,
    problem: null,
  });
  fireEvent.focus(window);
  await waitFor(() =>
    expect(document.documentElement.dataset.theme).toBe(Theme.Dark),
  );
  vi.mocked(value.bridge.update).mockRejectedValueOnce(
    new Error("uncertain delivery"),
  );
  fireEvent.click(screen.getByRole("radio", { name: "Light" }));
  await screen.findByRole("button", { name: "Reload appearance" });
  vi.mocked(value.bridge.read).mockClear();
  fireEvent.focus(window);
  expect(value.bridge.read).not.toHaveBeenCalled();
});

test("v2 validates custom theme imports and preserves immutable defaults", async () => {
  const { defaultPreferences, parsePreferences, parseThemeFile, palettes } =
    await import("./appearance-preferences");
  const original = defaultPreferences();
  expect(parsePreferences(original)).toEqual(original);
  const theme = {
    version: 1,
    name: "Fixture",
    ...structuredClone(palettes.default),
  };
  expect(parseThemeFile(JSON.stringify(theme))).toEqual(theme);
  for (const value of [
    { ...theme, script: "alert(1)" },
    { ...theme, light: { ...theme.light, text: "#FFFFFF" } },
    { ...theme, light: { ...theme.light, accent: "url(private)" } },
    { ...theme, name: "x".repeat(81) },
  ])
    expect(() => parseThemeFile(JSON.stringify(value))).toThrow();
  expect(() => parseThemeFile(" ".repeat(32769))).toThrow();
  expect(() =>
    parsePreferences({ ...original, light_palette: "foreign" }),
  ).toThrow();
});

test("exact key checks reject delimiter-collision, unknown, missing and duplicate-style key sets", async () => {
  const { defaultPreferences, parsePreferences, parseThemeFile, palettes } =
    await import("./appearance-preferences");
  const { validColors, validTheme } = await import("./appearance-preferences");
  const base = palettes.default.light;
  // One key that embeds the old delimiter replaces two real keys. The key count
  // differs, so joining sorted keys with commas used to make these look equal.
  const { background, border, ...withoutPair } = base;
  const collided = {
    ...withoutPair,
    "background,border": background,
  };
  expect(validColors(base)).toBe(true);
  expect(validColors(collided)).toBe(false);
  // Same key count with one missing token and one unknown token.
  const { link, ...withoutLink } = base;
  expect(validColors({ ...withoutLink, extra: link })).toBe(false);
  expect(validColors(withoutLink)).toBe(false);
  expect(validColors({ ...base, extra: "#000000" })).toBe(false);
  // A reversed key order is still the same complete set.
  expect(validColors(Object.fromEntries(Object.entries(base).reverse()))).toBe(
    true,
  );

  const theme = {
    version: 1,
    name: "Fixture",
    ...structuredClone(palettes.default),
  };
  const identity = { ...theme, id: "01900000-0000-7000-8000-000000000001" };
  for (const mode of ["light", "dark"] as const) {
    const bad = { ...theme, [mode]: collided };
    expect(validTheme(bad)).toBe(false);
    expect(validTheme({ ...identity, [mode]: collided }, true)).toBe(false);
    expect(() => parseThemeFile(JSON.stringify(bad))).toThrow();
  }
  expect(validTheme(theme)).toBe(true);
  expect(validTheme(identity, true)).toBe(true);
  expect(validTheme({ ...theme, id: identity.id })).toBe(false);
  expect(validTheme(theme, true)).toBe(false);
  expect(validTheme({ ...identity, extra: true }, true)).toBe(false);
  const { dark, ...withoutDark } = theme;
  expect(validTheme({ ...withoutDark, "dark,id": dark })).toBe(false);
  expect(
    validTheme(
      { version: 1, name: "Fixture", light: theme.light, "dark,name": dark },
      false,
    ),
  ).toBe(false);
  expect(
    validTheme({ ...identity, light: collided, dark: theme.dark }, true),
  ).toBe(false);

  const preferences = defaultPreferences();
  expect(parsePreferences(preferences)).toEqual(preferences);
  const { density, composer_size, ...withoutPair2 } = preferences;
  expect(() =>
    parsePreferences({
      ...withoutPair2,
      "density,composer_size": density,
    }),
  ).toThrow();
  expect(() => parsePreferences(withoutPair2)).toThrow();
  expect(() => parsePreferences({ ...preferences, extra: true })).toThrow();
  expect(() =>
    parsePreferences({ ...withoutPair2, composer_size, extra: density }),
  ).toThrow();
  expect(() =>
    parsePreferences({
      ...preferences,
      custom_themes: [{ ...identity, light: collided }],
    }),
  ).toThrow();
});

test("the custom theme preview ignores color maps that are not an exact complete set", async () => {
  const { palettes } = await import("./appearance-preferences");
  const { ColorPreview } = await import("./appearance-controls");
  const OriginalSheet = CSSStyleSheet;
  vi.stubGlobal(
    "CSSStyleSheet",
    class extends OriginalSheet {
      replaceSync(source: string) {
        this.insertRule(source);
      }
    },
  );
  const descriptor = Object.getOwnPropertyDescriptor(
    document,
    "adoptedStyleSheets",
  );
  Object.defineProperty(document, "adoptedStyleSheets", {
    configurable: true,
    writable: true,
    value: [],
  });
  try {
    const valid = {
      version: 1 as const,
      id: "019abcde-1234-7123-8123-123456789abc",
      name: "Fixture",
      ...palettes.default,
    };
    const { background, border, ...withoutPair } = palettes.default.light;
    const collided = {
      ...withoutPair,
      "background,border": background,
    } as unknown as typeof palettes.default.light;
    const accepted = render(<ColorPreview draft={valid} />);
    expect(document.adoptedStyleSheets).toHaveLength(2);
    accepted.unmount();
    expect(document.adoptedStyleSheets).toHaveLength(0);
    render(<ColorPreview draft={{ ...valid, light: collided }} />);
    expect(document.adoptedStyleSheets).toHaveLength(0);
    const { id: _, ...withoutId } = valid;
    render(<ColorPreview draft={withoutId as unknown as typeof valid} />);
    expect(document.adoptedStyleSheets).toHaveLength(0);
  } finally {
    if (descriptor)
      Object.defineProperty(document, "adoptedStyleSheets", descriptor);
    else Reflect.deleteProperty(document, "adoptedStyleSheets");
  }
});

test("selected palette sheets follow System changes and retire only their own sheet", async () => {
  const { defaultPreferences, palettes } =
    await import("./appearance-preferences");
  const changeScheme = scheme(false);
  // jsdom has no constructed sheets. This shim checks ownership and lifecycle;
  // scripts/test-appearance-palette.mjs checks the actual browser cascade.
  const OriginalSheet = CSSStyleSheet;
  vi.stubGlobal(
    "CSSStyleSheet",
    class extends OriginalSheet {
      replaceSync(source: string) {
        this.insertRule(source);
      }
    },
  );
  const descriptor = Object.getOwnPropertyDescriptor(
    document,
    "adoptedStyleSheets",
  );
  const unrelated = new CSSStyleSheet();
  Object.defineProperty(document, "adoptedStyleSheets", {
    configurable: true,
    writable: true,
    value: [unrelated],
  });
  const preferences = {
    ...defaultPreferences(),
    light_palette: "nord",
    dark_palette: "dracula",
  };
  const value = fixture();
  vi.mocked(value.bridge.read).mockResolvedValue({
    revision: 1,
    theme: Theme.System,
    problem: null,
    preferences,
  });
  const view = render(
    <AppearanceProvider bridge={value.bridge}>
      <textarea
        aria-label="Palette draft"
        defaultValue="retained palette draft"
      />
    </AppearanceProvider>,
  );
  const draft = screen.getByRole("textbox", { name: "Palette draft" });
  const currentRule = () =>
    document.adoptedStyleSheets[1].cssRules[0] as CSSStyleRule;
  try {
    await waitFor(() =>
      expect(currentRule().style.getPropertyValue("--background")).toBe(
        palettes.nord.light.background,
      ),
    );
    const lightSheet = document.adoptedStyleSheets[1];
    expect(currentRule().selectorText).toBe("html:root[data-theme]");
    changeScheme(true);
    expect(currentRule().style.getPropertyValue("--background")).toBe(
      palettes.dracula.dark.background,
    );
    expect(document.adoptedStyleSheets).not.toContain(lightSheet);
    changeScheme(false);
    expect(currentRule().style.getPropertyValue("--background")).toBe(
      palettes.nord.light.background,
    );
    expect(document.adoptedStyleSheets).toHaveLength(2);
    for (const [token, color] of Object.entries(palettes.nord.light))
      expect(currentRule().style.getPropertyValue(`--${token}`)).toBe(color);
    expect(screen.getByRole("textbox", { name: "Palette draft" })).toBe(draft);
    expect((draft as HTMLTextAreaElement).value).toBe("retained palette draft");
  } finally {
    view.unmount();
    const remaining = document.adoptedStyleSheets;
    if (descriptor)
      Object.defineProperty(document, "adoptedStyleSheets", descriptor);
    else Reflect.deleteProperty(document, "adoptedStyleSheets");
    expect(remaining).toEqual([unrelated]);
  }
});

test("v2 ordinary choices autosave full snapshots without replacing a draft", async () => {
  const { defaultPreferences } = await import("./appearance-preferences");
  scheme(false);
  const value = fixture();
  vi.mocked(value.bridge.read).mockResolvedValue({
    revision: 1,
    theme: Theme.System,
    problem: null,
    preferences: defaultPreferences(),
  });
  vi.mocked(value.bridge.update).mockImplementation(
    async (theme, revision, preferences) => ({
      theme,
      revision: revision + 1,
      problem: null,
      preferences,
    }),
  );
  render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
      <textarea aria-label="Persistent draft" defaultValue="original" />
    </AppearanceProvider>,
  );
  const density = await screen.findByRole("combobox", {
    name: "Display density",
  });
  await waitFor(() =>
    expect((density as HTMLSelectElement).matches(":disabled")).toBe(false),
  );
  fireEvent.change(density, { target: { value: "compact" } });
  await waitFor(() =>
    expect(document.documentElement.dataset.density).toBe("compact"),
  );
  expect(
    (
      screen.getByRole("textbox", {
        name: "Persistent draft",
      }) as HTMLTextAreaElement
    ).value,
  ).toBe("original");
  expect(
    (
      screen.getByRole("checkbox", { name: "Markdown" }) as HTMLInputElement
    ).matches(":disabled"),
  ).toBe(true);
});

test.each([
  { mode: Theme.Light, row: "light", light: "default", dark: "dracula" },
  { mode: Theme.Light, row: "dark", light: "dracula", dark: "default" },
  { mode: Theme.Dark, row: "light", light: "dracula", dark: "default" },
  { mode: Theme.Dark, row: "dark", light: "nord", dark: "nord" },
  { mode: Theme.System, row: "light", light: "nord", dark: "nord" },
  { mode: Theme.System, row: "dark", light: "default", dark: "dracula" },
])(
  "the $row palette row duplicates its selected family in $mode mode ($light/$dark)",
  async ({ mode, row, light, dark }) => {
    const { defaultPreferences, palettes, colorTokens } =
      await import("./appearance-preferences");
    scheme(true);
    const value = fixture(mode);
    const preferences = {
      ...defaultPreferences(),
      light_palette: light,
      dark_palette: dark,
    };
    const original = structuredClone(preferences);
    vi.mocked(value.bridge.read).mockResolvedValue({
      revision: 1,
      theme: mode,
      problem: null,
      preferences,
    });
    vi.mocked(value.bridge.update).mockImplementation(
      async (theme, revision, next) => ({
        theme,
        revision: revision + 1,
        problem: null,
        preferences: next,
      }),
    );
    render(
      <AppearanceProvider bridge={value.bridge}>
        <AppearanceSettings />
      </AppearanceProvider>,
    );
    const name = `Duplicate ${row} palette`;
    const button = await screen.findByRole("button", { name });
    await waitFor(() => expect(button.matches(":disabled")).toBe(false));
    const select = screen.getByRole("combobox", {
      name: `${row === "light" ? "Light" : "Dark"} palette`,
    }) as HTMLSelectElement;
    button.focus();
    fireEvent.click(button);
    const dialog = within(screen.getByRole("dialog"));
    const reference = row === "light" ? light : dark;
    const expected = palettes[reference as keyof typeof palettes];
    const [themeName, ...colorInputs] = dialog.getAllByRole(
      "textbox",
    ) as HTMLInputElement[];
    expect(themeName.value).toBe(select.selectedOptions[0].textContent);
    expect(
      Object.fromEntries(
        colorInputs.map((input) => [
          input.getAttribute("aria-label"),
          input.value,
        ]),
      ),
    ).toEqual(
      Object.fromEntries(
        colorTokens.flatMap((token) => [
          [`Light ${token}`, expected.light[token]],
          [`Dark ${token}`, expected.dark[token]],
        ]),
      ),
    );
    expect(value.bridge.update).not.toHaveBeenCalled();
    fireEvent.click(dialog.getByRole("button", { name: "Cancel" }));
    expect(document.activeElement).toBe(button);
    expect(preferences).toEqual(original);
  },
);

test("saving a bundled palette duplicate preserves both selected references", async () => {
  const { defaultPreferences, palettes } =
    await import("./appearance-preferences");
  scheme(true);
  const value = fixture(Theme.System);
  const preferences = {
    ...defaultPreferences(),
    light_palette: "default",
    dark_palette: "dracula",
  };
  vi.mocked(value.bridge.read).mockResolvedValue({
    revision: 1,
    theme: Theme.System,
    problem: null,
    preferences,
  });
  vi.mocked(value.bridge.update).mockImplementation(
    async (theme, revision, next) => ({
      theme,
      revision: revision + 1,
      problem: null,
      preferences: next,
    }),
  );
  render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  const duplicate = await screen.findByRole("button", {
    name: "Duplicate dark palette",
  });
  await waitFor(() => expect(duplicate.matches(":disabled")).toBe(false));
  fireEvent.click(duplicate);
  fireEvent.click(screen.getByRole("button", { name: "Save theme" }));
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
  const saved = vi.mocked(value.bridge.update).mock.calls[0][2]!;
  expect(saved.light_palette).toBe("default");
  expect(saved.dark_palette).toBe("dracula");
  expect(saved.custom_themes).toHaveLength(1);
  expect(saved.custom_themes[0]).toMatchObject({
    version: 1,
    light: palettes.dracula.light,
    dark: palettes.dracula.dark,
  });
  expect(saved.custom_themes[0].id).toMatch(
    /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
  );
  expect(preferences.custom_themes).toEqual([]);
});

test("selected custom palette and library row duplication preserve the original theme", async () => {
  const { defaultPreferences, palettes } =
    await import("./appearance-preferences");
  scheme(false);
  const value = fixture();
  const custom = {
    version: 1 as const,
    id: "019abcde-1234-7123-8123-123456789abc",
    name: "Original custom theme",
    ...structuredClone(palettes.dracula),
  };
  const preferences = {
    ...defaultPreferences(),
    dark_palette: custom.id,
    custom_themes: [custom],
  };
  const original = structuredClone(preferences);
  vi.mocked(value.bridge.read).mockResolvedValue({
    revision: 1,
    theme: Theme.System,
    problem: null,
    preferences,
  });
  vi.mocked(value.bridge.update).mockImplementation(
    async (theme, revision, next) => ({
      theme,
      revision: revision + 1,
      problem: null,
      preferences: next,
    }),
  );
  render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  const dark = await screen.findByRole("button", {
    name: "Duplicate dark palette",
  });
  await waitFor(() => expect(dark.matches(":disabled")).toBe(false));
  fireEvent.click(dark);
  expect(
    (screen.getByRole("textbox", { name: "Theme name" }) as HTMLInputElement)
      .value,
  ).toBe(custom.name);
  fireEvent.change(screen.getByRole("textbox", { name: "Light background" }), {
    target: { value: "#FFFFFF" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(preferences).toEqual(original);
  expect(value.bridge.update).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Duplicate theme" }));
  expect(
    (
      screen.getByRole("textbox", {
        name: "Light background",
      }) as HTMLInputElement
    ).value,
  ).toBe(custom.light.background);
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(value.bridge.update).not.toHaveBeenCalled();
  fireEvent.click(dark);
  fireEvent.click(screen.getByRole("button", { name: "Save theme" }));
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
  const saved = vi.mocked(value.bridge.update).mock.calls[0][2]!;
  expect(saved.custom_themes).toHaveLength(2);
  expect(saved.custom_themes[0]).toEqual(custom);
  expect(saved.custom_themes[1]).toEqual({
    ...custom,
    id: expect.any(String),
  });
  expect(saved.custom_themes[1].id).not.toBe(custom.id);
  expect(saved.light_palette).toBe(preferences.light_palette);
  expect(saved.dark_palette).toBe(custom.id);
  expect(preferences).toEqual(original);
  await waitFor(() =>
    expect(screen.queryByRole("textbox", { name: "Theme name" })).toBeNull(),
  );
});

test("palette duplication exposes distinct English and Korean action names", async () => {
  const { i18n } = await import("./localization");
  scheme(false);
  const value = fixture();
  render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  await screen.findByRole("button", { name: "Duplicate dark palette" });
  try {
    await act(() => i18n.changeLanguage("ko"));
    for (const name of ["라이트 팔레트 복제", "다크 팔레트 복제"])
      expect(screen.getByRole("button", { name }).textContent).toBe("복제");
    expect(value.bridge.update).not.toHaveBeenCalled();
  } finally {
    await act(() => i18n.changeLanguage("en"));
  }
});

test("both palette duplication actions retain the 32-theme library limit", async () => {
  const { defaultPreferences, palettes } =
    await import("./appearance-preferences");
  scheme(false);
  const value = fixture();
  const preferences = {
    ...defaultPreferences(),
    custom_themes: Array.from({ length: 32 }, (_, index) => ({
      version: 1 as const,
      id: `019abcde-1234-7123-8123-${String(index).padStart(12, "0")}`,
      name: `Custom theme ${index}`,
      ...structuredClone(palettes.default),
    })),
  };
  vi.mocked(value.bridge.read).mockResolvedValue({
    revision: 1,
    theme: Theme.System,
    problem: null,
    preferences,
  });
  render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  await screen.findByText("Custom theme 31", {
    selector: ".appearance-custom > span",
  });
  for (const name of ["Duplicate light palette", "Duplicate dark palette"])
    expect(screen.getByRole("button", { name }).matches(":disabled")).toBe(
      true,
    );
  expect(value.bridge.update).not.toHaveBeenCalled();
});

test("custom theme confirmation retains a draft on concurrent appearance commits", async () => {
  const { defaultPreferences } = await import("./appearance-preferences");
  scheme(false);
  const value = fixture();
  const preferences = defaultPreferences();
  vi.mocked(value.bridge.read).mockResolvedValue({
    revision: 1,
    theme: Theme.System,
    problem: null,
    preferences,
  });
  render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  const duplicate = await screen.findByRole("button", {
    name: "Duplicate light palette",
  });
  await waitFor(() =>
    expect((duplicate as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(duplicate);
  const name = screen.getByRole("textbox", { name: "Theme name" });
  fireEvent.change(name, { target: { value: "Retained custom draft" } });
  expect(value.bridge.update).not.toHaveBeenCalled();
  act(() =>
    value.publish({
      revision: 2,
      theme: Theme.Dark,
      problem: null,
      preferences,
    }),
  );
  expect((name as HTMLInputElement).value).toBe("Retained custom draft");
  expect(
    (screen.getByRole("button", { name: "Save theme" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(screen.queryByRole("textbox", { name: "Theme name" })).toBeNull();
  expect(value.bridge.update).not.toHaveBeenCalled();
});

test("custom theme save waits for the positively committed native map identity", async () => {
  const { defaultPreferences } = await import("./appearance-preferences");
  scheme(false);
  const value = fixture();
  const pending = deferred<unknown>();
  vi.mocked(value.bridge.read).mockResolvedValue({
    revision: 1,
    theme: Theme.System,
    problem: null,
    preferences: defaultPreferences(),
  });
  vi.mocked(value.bridge.update).mockReturnValueOnce(pending.promise);
  render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  const duplicate = await screen.findByRole("button", {
    name: "Duplicate light palette",
  });
  await waitFor(() =>
    expect((duplicate as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(duplicate);
  fireEvent.change(screen.getByRole("textbox", { name: "Theme name" }), {
    target: { value: "Confirmed theme" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save theme" }));
  expect(screen.getByRole("textbox", { name: "Theme name" })).toBeTruthy();
  const [theme, revision, preferences] = vi.mocked(value.bridge.update).mock
    .calls[0];
  const sorted = {
    ...preferences!,
    custom_themes: preferences!.custom_themes.map((t) => ({
      ...t,
      light: Object.fromEntries(Object.entries(t.light).sort()),
      dark: Object.fromEntries(Object.entries(t.dark).sort()),
    })),
  };
  await act(async () =>
    pending.resolve({
      theme,
      revision: revision + 1,
      problem: null,
      preferences: sorted,
    }),
  );
  expect(screen.queryByRole("textbox", { name: "Theme name" })).toBeNull();
  expect(screen.getAllByText("Confirmed theme").length).toBeGreaterThan(0);
});

// jsdom has no constructed sheets. This shim checks ownership and lifecycle
// only; scripts/test-appearance-palette.mjs checks the actual browser cascade.
function stubConstructedSheets() {
  const OriginalSheet = CSSStyleSheet;
  vi.stubGlobal(
    "CSSStyleSheet",
    class extends OriginalSheet {
      replaceSync(source: string) {
        this.insertRule(source);
      }
    },
  );
  const descriptor = Object.getOwnPropertyDescriptor(
    document,
    "adoptedStyleSheets",
  );
  Object.defineProperty(document, "adoptedStyleSheets", {
    configurable: true,
    writable: true,
    value: [],
  });
  return () => {
    if (descriptor)
      Object.defineProperty(document, "adoptedStyleSheets", descriptor);
    else Reflect.deleteProperty(document, "adoptedStyleSheets");
  };
}
const previewSheets = () =>
  document.adoptedStyleSheets.filter((sheet) =>
    (sheet.cssRules[0] as CSSStyleRule | undefined)?.selectorText?.startsWith(
      ".appearance-preview-",
    ),
  );
const previewRule = (mode: "light" | "dark") =>
  previewSheets().find(
    (sheet) =>
      (sheet.cssRules[0] as CSSStyleRule).selectorText ===
      `.appearance-preview-${mode}`,
  )!.cssRules[0] as CSSStyleRule;

async function openThemeEditor(value: ReturnType<typeof fixture>) {
  const { defaultPreferences } = await import("./appearance-preferences");
  vi.mocked(value.bridge.read).mockResolvedValue({
    revision: 1,
    theme: Theme.System,
    problem: null,
    preferences: defaultPreferences(),
  });
  const view = render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  const duplicate = await screen.findByRole("button", {
    name: "Duplicate light palette",
  });
  await waitFor(() =>
    expect((duplicate as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(duplicate);
  return view;
}
const editorInputs = () => screen.getAllByRole("textbox") as HTMLInputElement[];

test("a pending custom theme save freezes the draft and closes on the submitted snapshot", async () => {
  scheme(false);
  const value = fixture(),
    pending = deferred<unknown>();
  await openThemeEditor(value);
  vi.mocked(value.bridge.update).mockReturnValueOnce(pending.promise);
  fireEvent.change(screen.getByRole("textbox", { name: "Theme name" }), {
    target: { value: "Frozen draft" },
  });
  expect(editorInputs().every((input) => !input.disabled)).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Save theme" }));
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
  await waitFor(() =>
    expect(
      screen.getByRole("textbox", { name: "Theme name" }).matches(":disabled"),
    ).toBe(true),
  );
  // The name and every color input are frozen while the save is pending.
  const inputs = editorInputs();
  expect(inputs.length).toBeGreaterThan(1);
  expect(inputs.every((input) => input.disabled)).toBe(true);
  const [theme, revision, preferences] = vi.mocked(value.bridge.update).mock
    .calls[0];
  expect(preferences!.custom_themes).toHaveLength(1);
  expect(preferences!.custom_themes[0].name).toBe("Frozen draft");
  await act(async () =>
    pending.resolve({
      theme,
      revision: revision + 1,
      problem: null,
      preferences,
    }),
  );
  expect(screen.queryByRole("textbox", { name: "Theme name" })).toBeNull();
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
  expect(screen.getAllByText("Frozen draft").length).toBeGreaterThan(0);
});

test("a failed or uncertain custom theme save keeps the exact draft and recovery controls", async () => {
  scheme(false);
  const value = fixture(),
    pending = deferred<unknown>();
  await openThemeEditor(value);
  vi.mocked(value.bridge.update).mockReturnValueOnce(pending.promise);
  fireEvent.change(screen.getByRole("textbox", { name: "Theme name" }), {
    target: { value: "Uncertain draft" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save theme" }));
  await act(async () => pending.reject(new Error("native write failed")));
  const name = screen.getByRole("textbox", {
    name: "Theme name",
  }) as HTMLInputElement;
  expect(name.value).toBe("Uncertain draft");
  expect(editorInputs().every((input) => !input.disabled)).toBe(true);
  expect(
    (screen.getByRole("button", { name: "Save theme" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
  const reload = screen.getByRole("button", { name: "Reload appearance" });
  // Reload reveals that the uncertain save did commit the submitted theme, so
  // the editor reconciles against the submitted snapshot and closes.
  const [theme, revision, preferences] = vi.mocked(value.bridge.update).mock
    .calls[0];
  vi.mocked(value.bridge.read).mockResolvedValue({
    theme,
    revision: revision + 1,
    problem: null,
    preferences,
  });
  fireEvent.click(reload);
  await waitFor(() =>
    expect(screen.queryByRole("textbox", { name: "Theme name" })).toBeNull(),
  );
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
});

test("a revision published before submit blocks Save and never discards the draft", async () => {
  const { defaultPreferences } = await import("./appearance-preferences");
  scheme(false);
  const value = fixture();
  await openThemeEditor(value);
  const name = screen.getByRole("textbox", {
    name: "Theme name",
  }) as HTMLInputElement;
  fireEvent.change(name, { target: { value: "Conflicted draft" } });
  act(() =>
    value.publish({
      revision: 2,
      theme: Theme.Dark,
      problem: null,
      preferences: defaultPreferences(),
    }),
  );
  expect(
    screen.getByText(
      "Appearance changed. This draft is retained. Cancel and reload before saving.",
    ),
  ).toBeTruthy();
  expect(
    (screen.getByRole("button", { name: "Save theme" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  fireEvent.submit(name.closest("form")!);
  expect(value.bridge.update).not.toHaveBeenCalled();
  expect(name.value).toBe("Conflicted draft");
  expect(name.disabled).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(screen.queryByRole("textbox", { name: "Theme name" })).toBeNull();
  expect(value.bridge.update).not.toHaveBeenCalled();
});

test("the editor preview installs scoped sheets for an identity-bearing draft and follows edits", async () => {
  const { palettes } = await import("./appearance-preferences");
  scheme(false);
  const restore = stubConstructedSheets();
  try {
    const value = fixture();
    const view = await openThemeEditor(value);
    expect(previewSheets()).toHaveLength(2);
    for (const mode of ["light", "dark"] as const)
      for (const [token, color] of Object.entries(palettes.default[mode]))
        expect(previewRule(mode).style.getPropertyValue(`--${token}`)).toBe(
          color,
        );
    const before = previewSheets();
    const field = screen.getByRole("textbox", {
      name: "Light background",
    }) as HTMLInputElement;
    expect(field.value).toBe(palettes.default.light.background);
    fireEvent.change(field, { target: { value: "#FEFEFE" } });
    expect(previewSheets()).toHaveLength(2);
    expect(previewSheets().some((sheet) => before.includes(sheet))).toBe(false);
    expect(previewRule("light").style.getPropertyValue("--background")).toBe(
      "#FEFEFE",
    );
    expect(previewRule("dark").style.getPropertyValue("--background")).toBe(
      palettes.default.dark.background,
    );
    view.unmount();
    expect(previewSheets()).toHaveLength(0);
  } finally {
    restore();
  }
});

test("the editor preview installs nothing for an invalid token or contrast", async () => {
  const { palettes } = await import("./appearance-preferences");
  scheme(false);
  const restore = stubConstructedSheets();
  let view: ReturnType<typeof render> | undefined;
  try {
    const value = fixture();
    view = await openThemeEditor(value);
    expect(previewSheets()).toHaveLength(2);
    const field = screen.getByRole("textbox", {
      name: "Light text",
    }) as HTMLInputElement;
    // Not a six-digit opaque color.
    fireEvent.change(field, { target: { value: "red" } });
    expect(previewSheets()).toHaveLength(0);
    // A valid color that fails the text-on-surface contrast row.
    fireEvent.change(field, {
      target: { value: palettes.default.light.surface },
    });
    expect(previewSheets()).toHaveLength(0);
    fireEvent.change(field, {
      target: { value: palettes.default.light.text },
    });
    expect(previewSheets()).toHaveLength(2);
  } finally {
    // Unmount while the shim is installed so cleanup finds the sheet list.
    view?.unmount();
    restore();
  }
});

test("the editor preview rejects an invalid identity and strict no-ID files still validate separately", async () => {
  const { palettes, parseThemeFile, validTheme } =
    await import("./appearance-preferences");
  const { ColorPreview } = await import("./appearance-controls");
  const restore = stubConstructedSheets();
  const mounted: ReturnType<typeof render>[] = [];
  try {
    const file = { version: 1 as const, name: "Fixture", ...palettes.default };
    const valid = { ...file, id: "019abcde-1234-7123-8123-123456789abc" };
    expect(validTheme(valid, true)).toBe(true);
    expect(validTheme(valid)).toBe(false);
    expect(validTheme(file)).toBe(true);
    expect(parseThemeFile(JSON.stringify(file)).name).toBe("Fixture");
    expect(() => parseThemeFile(JSON.stringify(valid))).toThrow();
    mounted.push(
      render(<ColorPreview draft={{ ...valid, id: "not-a-uuid" }} />),
    );
    expect(previewSheets()).toHaveLength(0);
    // A version 4 UUID is not the required UUIDv7 identity.
    mounted.push(
      render(
        <ColorPreview
          draft={{ ...valid, id: "123e4567-e89b-42d3-a456-426614174000" }}
        />,
      ),
    );
    expect(previewSheets()).toHaveLength(0);
    expect(document.adoptedStyleSheets).toHaveLength(0);
  } finally {
    mounted.forEach((view) => view.unmount());
    restore();
  }
});

test("keeps described settings, separate disclosure choices and unavailable renderers", async () => {
  scheme(false);
  const value = fixture();
  const view = render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  await screen.findByText("Theme saved.");
  expect(screen.getByText("Changes save automatically.")).toBeTruthy();
  expect(
    Array.from(
      view.container.querySelectorAll(
        ".appearance-settings > fieldset > legend",
      ),
    ).map((legend) => legend.textContent),
  ).toEqual(["Theme", "Composer", "Session status", "Display", "Images"]);
  for (const control of view.container.querySelectorAll(
    ".appearance-row input, .appearance-row select",
  )) {
    const label = control.getAttribute("aria-labelledby");
    const help = control.getAttribute("aria-describedby");
    expect(label && document.getElementById(label)?.textContent).toBeTruthy();
    expect(help && document.getElementById(help)?.textContent).toBeTruthy();
  }
  const disclosures = view.container.querySelector(
    ".appearance-disclosure-controls",
  )!;
  expect(Array.from(disclosures.querySelectorAll("select"))).toHaveLength(3);
  for (const name of [
    "Tool details default",
    "Reasoning default",
    "Compaction history default",
  ])
    expect(
      within(disclosures as HTMLElement).getByRole("combobox", { name }),
    ).toBeTruthy();
  for (const name of [
    "Markdown",
    "Mermaid diagrams",
    "SVG visualizations",
    "Automatic table charts",
  ])
    expect(
      (screen.getByRole("checkbox", { name }) as HTMLInputElement).matches(
        ":disabled",
      ),
    ).toBe(true);
  expect(value.bridge.update).not.toHaveBeenCalled();
});

function ReadyProbe() {
  return <p data-testid="ready">{String(useAppearanceReady())}</p>;
}
test("readiness stays false until the initial read settles, including a failed read", async () => {
  scheme(false);
  const value = fixture(),
    initial = deferred<unknown>();
  vi.mocked(value.bridge.read).mockReturnValueOnce(initial.promise);
  const mounted = render(
    <AppearanceProvider bridge={value.bridge}>
      <ReadyProbe />
    </AppearanceProvider>,
  );
  await waitFor(() => expect(value.bridge.read).toHaveBeenCalled());
  expect(screen.getByTestId("ready").textContent).toBe("false");
  await act(async () =>
    initial.resolve({ revision: 1, theme: Theme.Light, problem: null }),
  );
  expect(screen.getByTestId("ready").textContent).toBe("true");
  mounted.unmount();

  const failing = fixture(),
    rejected = deferred<unknown>();
  vi.mocked(failing.bridge.read).mockReturnValueOnce(rejected.promise);
  render(
    <AppearanceProvider bridge={failing.bridge}>
      <ReadyProbe />
    </AppearanceProvider>,
  );
  await waitFor(() => expect(failing.bridge.read).toHaveBeenCalled());
  expect(screen.getByTestId("ready").textContent).toBe("false");
  await act(async () => rejected.reject(new Error("unavailable")));
  expect(screen.getByTestId("ready").textContent).toBe("true");
});

test("custom theme deletion opener and confirmation are Danger while Cancel, Edit and Export stay neutral", async () => {
  const { defaultPreferences, palettes } =
    await import("./appearance-preferences");
  scheme(false);
  const value = fixture();
  const custom = {
    version: 1 as const,
    id: "019abcde-1234-7123-8123-123456789abc",
    name: "Deletable theme",
    ...structuredClone(palettes.dracula),
  };
  const preferences = { ...defaultPreferences(), custom_themes: [custom] };
  vi.mocked(value.bridge.read).mockResolvedValue({
    revision: 1,
    theme: Theme.System,
    problem: null,
    preferences,
  });
  vi.mocked(value.bridge.update).mockImplementation(
    async (theme, revision, next) => ({
      theme,
      revision: revision + 1,
      problem: null,
      preferences: next,
    }),
  );
  render(
    <AppearanceProvider bridge={value.bridge}>
      <AppearanceSettings />
    </AppearanceProvider>,
  );
  const opener = await screen.findByRole("button", { name: "Delete theme" });
  expectTone(opener, danger);
  expectTone(
    screen.getByRole("button", { name: "Edit custom theme" }),
    neutral,
  );
  expectTone(screen.getByRole("button", { name: "Export theme" }), neutral);
  await waitFor(() => expect(opener.matches(":disabled")).toBe(false));
  fireEvent.click(opener);
  expect(value.bridge.update).not.toHaveBeenCalled();
  const dialog = within(screen.getByRole("dialog"));
  expectTone(dialog.getByRole("button", { name: "Confirm deletion" }), danger);
  expectTone(dialog.getByRole("button", { name: "Cancel" }), neutral);
});

test.each(Object.values(AppearanceProblem))(
  "a %s problem renders no global alert outside Settings and one local alert with Reload inside",
  async (problem) => {
    scheme(false);
    const value = fixture();
    vi.mocked(value.bridge.read).mockResolvedValue({
      revision: 1,
      theme: Theme.System,
      problem,
    });
    const main = render(
      <AppearanceProvider bridge={value.bridge}>
        <p>Main content</p>
      </AppearanceProvider>,
    );
    await waitFor(() => expect(value.bridge.read).toHaveBeenCalled());
    await act(async () => {});
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryByRole("status")).toBeNull();
    expect(main.container.textContent).toBe("Main content");
    main.unmount();
    render(
      <AppearanceProvider bridge={value.bridge}>
        <AppearanceSettings />
      </AppearanceProvider>,
    );
    await waitFor(() => expect(screen.getAllByRole("alert")).toHaveLength(1));
    expect(
      screen.getByRole("button", { name: "Reload appearance" }),
    ).toBeTruthy();
  },
);
