// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useState } from "react";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { LanguagePreference, LanguageProblem, LanguageProvider, LanguageSettings, parseLanguage, type LanguageBridge, type LanguageSnapshot } from "./language";
import { i18n, SupportedLanguage } from "./localization";

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function choose(input: HTMLInputElement, label: string) {
  fireEvent.click(input);
  fireEvent.click(screen.getByRole("option", { name: label }));
}
function fixture(resolved = SupportedLanguage.English) {
  let snapshot: LanguageSnapshot = { revision: 1, language: LanguagePreference.System, resolved_language: resolved, problem: null };
  const listeners = new Set<(value: unknown) => void>();
  const publish = (next: LanguageSnapshot) => { snapshot = next; listeners.forEach(changed => changed(next)); };
  const bridge: LanguageBridge = {
    read: vi.fn(async () => snapshot),
    update: vi.fn(async (language, revision) => {
      if (revision !== snapshot.revision) return { ...snapshot, problem: LanguageProblem.Changed };
      const next = { revision: revision + 1, language, resolved_language: language === LanguagePreference.Korean ? SupportedLanguage.Korean : SupportedLanguage.English, problem: null };
      publish(next); return next;
    }),
    subscribe: vi.fn(async changed => { listeners.add(changed); return () => { listeners.delete(changed); }; }),
  };
  return { bridge, listeners, publish };
}

test("selecting language preserves the mounted draft and current focus", async () => {
  const value = fixture();
  render(<LanguageProvider bridge={value.bridge}><LanguageSettings /><textarea aria-label="Original draft" defaultValue="한국어 user content <b>inert</b>" /></LanguageProvider>);
  const select = screen.getByRole("combobox", { name: "Language" }) as HTMLInputElement;
  await waitFor(() => expect(select.disabled).toBe(false));
  act(() => select.focus()); choose(select, "Korean - 한국어");
  await screen.findByText("언어를 저장했습니다.");
  expect(document.documentElement.lang).toBe("ko");
  expect(document.activeElement).toBe(select);
  expect(screen.getByRole("textbox", { name: "Original draft" })).toHaveProperty("value", "한국어 user content <b>inert</b>");
  expect(value.bridge.update).toHaveBeenCalledExactlyOnceWith(LanguagePreference.Korean, 1);
});

test("old reads and events cannot replace a newer language", async () => {
  const value = fixture(), initial = deferred<unknown>();
  vi.mocked(value.bridge.read).mockReturnValueOnce(initial.promise);
  render(<LanguageProvider bridge={value.bridge}><LanguageSettings /></LanguageProvider>);
  await waitFor(() => expect(value.bridge.read).toHaveBeenCalled());
  act(() => value.publish({ revision: 7, language: LanguagePreference.Korean, resolved_language: SupportedLanguage.Korean, problem: null }));
  await act(async () => initial.resolve({ revision: 1, language: LanguagePreference.English, resolved_language: SupportedLanguage.English, problem: null }));
  act(() => value.publish({ revision: 3, language: LanguagePreference.English, resolved_language: SupportedLanguage.English, problem: null }));
  expect(document.documentElement.lang).toBe("ko");
  expect(i18n.resolvedLanguage).toBe("ko");
});

test("uncertain saves retain the commit and need explicit inspection", async () => {
  const value = fixture();
  vi.mocked(value.bridge.update).mockRejectedValueOnce(new Error("private path and diagnostic"));
  render(<LanguageProvider bridge={value.bridge}><LanguageSettings /></LanguageProvider>);
  const select = screen.getByRole("combobox", { name: "Language" }) as HTMLInputElement;
  await waitFor(() => expect(select.disabled).toBe(false));
  choose(select, "Korean - 한국어");
  const reload = await screen.findByRole("button", { name: "Reload language" });
  expect(select.value).toBe("Follow system");
  expect(select.disabled).toBe(true);
  expect(screen.queryByText(/private path/)).toBeNull();
  vi.mocked(value.bridge.read).mockClear(); fireEvent.focus(window);
  expect(value.bridge.read).not.toHaveBeenCalled();
  fireEvent.click(reload);
  await waitFor(() => expect(select.disabled).toBe(false));
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
});

test("committed events update other controllers and newly opened windows", async () => {
  const value = fixture();
  const first = render(<LanguageProvider bridge={value.bridge}><LanguageSettings /></LanguageProvider>);
  const second = render(<LanguageProvider bridge={value.bridge}><LanguageSettings /></LanguageProvider>);
  const select = within(first.container).getByRole("combobox", { name: "Language" }) as HTMLInputElement;
  await waitFor(() => expect(select.disabled).toBe(false));
  await waitFor(() => expect((within(second.container).getByRole("combobox") as HTMLInputElement).disabled).toBe(false));
  choose(select, "Korean - 한국어");
  await waitFor(() => expect((within(second.container).getByRole("combobox", { name: "언어" }) as HTMLInputElement).value).toBe("Korean - 한국어"));
  first.unmount(); second.unmount();
  render(<LanguageProvider bridge={value.bridge}><LanguageSettings /></LanguageProvider>);
  await screen.findByText("언어를 저장했습니다.");
});

test("Settings departure and Strict Mode do not cancel the device save", async () => {
  const value = fixture(), update = deferred<unknown>();
  vi.mocked(value.bridge.update).mockReturnValueOnce(update.promise);
  function Opening() { const [open, setOpen] = useState(true); return <><button onClick={() => setOpen(!open)}>Toggle</button>{open ? <LanguageSettings /> : null}</>; }
  render(<StrictMode><LanguageProvider bridge={value.bridge}><Opening /></LanguageProvider></StrictMode>);
  const select = screen.getByRole("combobox", { name: "Language" }) as HTMLInputElement;
  await waitFor(() => expect(select.disabled).toBe(false));
  choose(select, "Korean - 한국어"); fireEvent.click(screen.getByText("Toggle"));
  await act(async () => update.resolve({ revision: 2, language: LanguagePreference.Korean, resolved_language: SupportedLanguage.Korean, problem: null }));
  fireEvent.click(screen.getByText("Toggle"));
  expect(screen.getByRole("combobox", { name: "언어" })).toHaveProperty("value", "Korean - 한국어");
  expect(value.listeners.size).toBe(1);
});

test("foreground inspection follows a changed OS language without a write", async () => {
  const value = fixture();
  render(<LanguageProvider bridge={value.bridge}><LanguageSettings /></LanguageProvider>);
  await waitFor(() => expect((screen.getByRole("combobox") as HTMLInputElement).disabled).toBe(false));
  vi.mocked(value.bridge.read).mockResolvedValue({ revision: 2, language: LanguagePreference.System, resolved_language: SupportedLanguage.Korean, problem: null });
  fireEvent.focus(window);
  await screen.findByText("언어를 저장했습니다.");
  expect(value.bridge.update).not.toHaveBeenCalled();
});

test("native snapshots reject unknown fields, invalid revisions and inconsistent choices", () => {
  const valid = { revision: 1, language: LanguagePreference.English, resolved_language: SupportedLanguage.English, problem: null };
  for (const value of [null, {}, { ...valid, revision: -1 }, { ...valid, revision: 1.5 }, { ...valid, language: "other" }, { ...valid, resolved_language: "ja" }, { ...valid, resolved_language: "ko" }, { ...valid, problem: "raw" }, { ...valid, widget_problem: "path" }, { ...valid, extra: true }]) expect(() => parseLanguage(value)).toThrow();
});

test.each([SupportedLanguage.English, SupportedLanguage.Korean])("self-names and English ordering stay fixed in %s", async language => {
  const value = fixture(language);
  render(<LanguageProvider bridge={value.bridge}><LanguageSettings /></LanguageProvider>);
  const input = screen.getByRole("combobox") as HTMLInputElement;
  await waitFor(() => expect(input.disabled).toBe(false));
  const system = language === SupportedLanguage.English ? "Follow system" : "시스템 설정 따르기";
  expect(input.value).toBe(system);
  fireEvent.click(input);
  expect(input.value).toBe("");
  expect(screen.getAllByRole("option").map(option => option.textContent)).toEqual([system, "English - English", "Korean - 한국어"]);
  expect(input.getAttribute("aria-controls")).toBe(screen.getByRole("listbox").id);
  expect(screen.getByRole("option", { name: system }).getAttribute("aria-describedby")).toBeTruthy();
  expect(screen.getByRole("option", { name: system }).querySelector("svg")).not.toBeNull();
  expect(value.bridge.update).not.toHaveBeenCalled();
});

test("English and native substring search only saves an explicitly confirmed enum", async () => {
  const value = fixture();
  render(<LanguageProvider bridge={value.bridge}><LanguageSettings /></LanguageProvider>);
  const input = screen.getByRole("combobox") as HTMLInputElement;
  await waitFor(() => expect(input.disabled).toBe(false));
  act(() => input.focus());
  for (const query of ["  kOr  ", "한국", "한"]) {
    fireEvent.change(input, { target: { value: query } });
    expect(screen.getAllByRole("option").map(option => option.textContent)).toEqual(["Korean - 한국어"]);
    expect(input.getAttribute("aria-activedescendant")).toBe(screen.getByRole("option").id);
    expect(screen.getByRole("option").querySelector("svg")).toBeNull();
  }
  expect(value.bridge.update).not.toHaveBeenCalled();
  fireEvent.keyDown(input, { key: "Enter" });
  await screen.findByText("언어를 저장했습니다.");
  expect(value.bridge.update).toHaveBeenCalledExactlyOnceWith(LanguagePreference.Korean, 1);
  expect(input.value).toBe("Korean - 한국어");
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(document.activeElement).toBe(input);
});

test("navigation stops at the ends and reselecting the saved choice does not write", async () => {
  const value = fixture();
  render(<LanguageProvider bridge={value.bridge}><LanguageSettings /></LanguageProvider>);
  const input = screen.getByRole("combobox") as HTMLInputElement;
  await waitFor(() => expect(input.disabled).toBe(false));
  fireEvent.keyDown(input, { key: "ArrowUp" });
  expect(screen.getByRole("option", { selected: true }).textContent).toBe("Korean - 한국어");
  fireEvent.keyDown(input, { key: "ArrowDown" });
  expect(screen.getByRole("option", { selected: true }).textContent).toBe("Korean - 한국어");
  for (let index = 0; index < 3; index++) fireEvent.keyDown(input, { key: "ArrowUp" });
  expect(screen.getByRole("option", { selected: true }).textContent).toBe("Follow system");
  expect(value.bridge.update).not.toHaveBeenCalled();
  fireEvent.keyDown(input, { key: "Enter" });
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(input.value).toBe("Follow system");
  expect(value.bridge.update).not.toHaveBeenCalled();
});

test.each(["Escape", "Tab", "outside pointer", "blur", "toggle"])("%s discards search without saving", async action => {
  const value = fixture();
  render(<LanguageProvider bridge={value.bridge}><LanguageSettings /></LanguageProvider>);
  const input = screen.getByRole("combobox") as HTMLInputElement;
  await waitFor(() => expect(input.disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Show language options" }));
  fireEvent.change(input, { target: { value: "korean" } });
  if (action === "outside pointer") fireEvent.pointerDown(document.body);
  else if (action === "blur") fireEvent.blur(input, { relatedTarget: null });
  else if (action === "toggle") fireEvent.click(screen.getByRole("button", { name: "Hide language options" }));
  else fireEvent.keyDown(input, { key: action });
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(input.value).toBe("Follow system");
  expect(value.bridge.update).not.toHaveBeenCalled();
  fireEvent.click(input);
  expect(input.value).toBe("");
  expect(screen.getAllByRole("option")).toHaveLength(3);
});

test("unmatched free text cannot become a language preference", async () => {
  const value = fixture();
  render(<LanguageProvider bridge={value.bridge}><LanguageSettings /></LanguageProvider>);
  const input = screen.getByRole("combobox") as HTMLInputElement;
  await waitFor(() => expect(input.disabled).toBe(false));
  fireEvent.change(input, { target: { value: "unsupported language" } });
  expect(screen.getByText("No matching languages.")).toBeTruthy();
  expect(screen.queryAllByRole("option")).toHaveLength(0);
  expect(input.hasAttribute("aria-activedescendant")).toBe(false);
  fireEvent.keyDown(input, { key: "ArrowDown" });
  fireEvent.keyDown(input, { key: "Enter" });
  expect(value.bridge.update).not.toHaveBeenCalled();
  fireEvent.change(input, { target: { value: "" } });
  expect(screen.queryByText("No matching languages.")).toBeNull();
  expect(screen.getAllByRole("option")).toHaveLength(3);
});

test("composition and legacy IME keys never navigate or confirm", async () => {
  const value = fixture();
  render(<LanguageProvider bridge={value.bridge}><LanguageSettings /></LanguageProvider>);
  const input = screen.getByRole("combobox") as HTMLInputElement;
  await waitFor(() => expect(input.disabled).toBe(false));
  fireEvent.change(input, { target: { value: "한" } });
  const active = input.getAttribute("aria-activedescendant");
  for (const key of ["Enter", "ArrowUp", "Escape"]) {
    fireEvent.keyDown(input, { key, isComposing: true });
    fireEvent.keyDown(input, { key, keyCode: 229 });
  }
  expect(value.bridge.update).not.toHaveBeenCalled();
  expect(input.value).toBe("한");
  expect(input.getAttribute("aria-activedescendant")).toBe(active);
  fireEvent.keyDown(input, { key: "Enter" });
  await screen.findByText("언어를 저장했습니다.");
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
});

test("native reads and saves lock both controls and cannot reopen the list", async () => {
  const value = fixture(), initial = deferred<unknown>(), update = deferred<unknown>();
  vi.mocked(value.bridge.read).mockReturnValueOnce(initial.promise);
  vi.mocked(value.bridge.update).mockReturnValueOnce(update.promise);
  render(<LanguageProvider bridge={value.bridge}><LanguageSettings /></LanguageProvider>);
  const input = screen.getByRole("combobox") as HTMLInputElement;
  const toggle = screen.getByRole("button", { name: "Show language options" }) as HTMLButtonElement;
  expect(input.disabled).toBe(true); expect(toggle.disabled).toBe(true);
  fireEvent.click(toggle); expect(screen.queryByRole("listbox")).toBeNull();
  await act(async () => initial.resolve({ revision: 1, language: LanguagePreference.System, resolved_language: SupportedLanguage.English, problem: null }));
  choose(input, "Korean - 한국어");
  expect(input.readOnly).toBe(true); expect(input.getAttribute("aria-disabled")).toBe("true"); expect(toggle.disabled).toBe(true);
  expect(document.activeElement).toBe(input);
  expect(input.value).toBe("Follow system");
  fireEvent.click(toggle); expect(screen.queryByRole("listbox")).toBeNull();
  fireEvent.change(input, { target: { value: "eng" } });
  fireEvent.keyDown(input, { key: "ArrowDown" });
  expect(input.value).toBe("Follow system");
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
  await act(async () => update.resolve({ revision: 2, language: LanguagePreference.Korean, resolved_language: SupportedLanguage.Korean, problem: null }));
  expect(input.disabled).toBe(false); expect(toggle.disabled).toBe(false);
  expect(input.readOnly).toBe(false);
  expect(input.value).toBe("Korean - 한국어");
  expect(screen.queryByRole("listbox")).toBeNull();
});

test("a newer window event discards the local search and keeps the input mounted", async () => {
  const value = fixture();
  render(<LanguageProvider bridge={value.bridge}><LanguageSettings /></LanguageProvider>);
  const input = screen.getByRole("combobox") as HTMLInputElement;
  await waitFor(() => expect(input.disabled).toBe(false));
  act(() => input.focus());
  fireEvent.change(input, { target: { value: "eng" } });
  act(() => value.publish({ revision: 2, language: LanguagePreference.Korean, resolved_language: SupportedLanguage.Korean, problem: null }));
  expect(screen.getByRole("combobox", { name: "언어" })).toBe(input);
  expect(input.value).toBe("Korean - 한국어");
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(document.activeElement).toBe(input);
  expect(value.bridge.update).not.toHaveBeenCalled();
});
