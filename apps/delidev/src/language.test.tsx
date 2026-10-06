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
function fixture() {
  let snapshot: LanguageSnapshot = { revision: 1, language: LanguagePreference.System, resolved_language: SupportedLanguage.English, problem: null };
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
  const select = screen.getByRole("combobox", { name: "Language" }) as HTMLSelectElement;
  await waitFor(() => expect(select.disabled).toBe(false));
  select.focus(); fireEvent.change(select, { target: { value: LanguagePreference.Korean } });
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
  const select = screen.getByRole("combobox", { name: "Language" }) as HTMLSelectElement;
  await waitFor(() => expect(select.disabled).toBe(false));
  fireEvent.change(select, { target: { value: LanguagePreference.Korean } });
  const reload = await screen.findByRole("button", { name: "Reload language" });
  expect(select.value).toBe(LanguagePreference.System);
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
  const select = within(first.container).getByRole("combobox", { name: "Language" }) as HTMLSelectElement;
  await waitFor(() => expect(select.disabled).toBe(false));
  await waitFor(() => expect((within(second.container).getByRole("combobox") as HTMLSelectElement).disabled).toBe(false));
  fireEvent.change(select, { target: { value: LanguagePreference.Korean } });
  await waitFor(() => expect((within(second.container).getByRole("combobox", { name: "언어" }) as HTMLSelectElement).value).toBe(LanguagePreference.Korean));
  first.unmount(); second.unmount();
  render(<LanguageProvider bridge={value.bridge}><LanguageSettings /></LanguageProvider>);
  await screen.findByText("언어를 저장했습니다.");
});

test("Settings departure and Strict Mode do not cancel the device save", async () => {
  const value = fixture(), update = deferred<unknown>();
  vi.mocked(value.bridge.update).mockReturnValueOnce(update.promise);
  function Opening() { const [open, setOpen] = useState(true); return <><button onClick={() => setOpen(!open)}>Toggle</button>{open ? <LanguageSettings /> : null}</>; }
  render(<StrictMode><LanguageProvider bridge={value.bridge}><Opening /></LanguageProvider></StrictMode>);
  const select = screen.getByRole("combobox", { name: "Language" }) as HTMLSelectElement;
  await waitFor(() => expect(select.disabled).toBe(false));
  fireEvent.change(select, { target: { value: LanguagePreference.Korean } }); fireEvent.click(screen.getByText("Toggle"));
  await act(async () => update.resolve({ revision: 2, language: LanguagePreference.Korean, resolved_language: SupportedLanguage.Korean, problem: null }));
  fireEvent.click(screen.getByText("Toggle"));
  expect(screen.getByRole("combobox", { name: "언어" })).toHaveProperty("value", LanguagePreference.Korean);
  expect(value.listeners.size).toBe(1);
});

test("foreground inspection follows a changed OS language without a write", async () => {
  const value = fixture();
  render(<LanguageProvider bridge={value.bridge}><LanguageSettings /></LanguageProvider>);
  await waitFor(() => expect((screen.getByRole("combobox") as HTMLSelectElement).disabled).toBe(false));
  vi.mocked(value.bridge.read).mockResolvedValue({ revision: 2, language: LanguagePreference.System, resolved_language: SupportedLanguage.Korean, problem: null });
  fireEvent.focus(window);
  await screen.findByText("언어를 저장했습니다.");
  expect(value.bridge.update).not.toHaveBeenCalled();
});

test("native snapshots reject unknown fields, invalid revisions and inconsistent choices", () => {
  const valid = { revision: 1, language: LanguagePreference.English, resolved_language: SupportedLanguage.English, problem: null };
  for (const value of [null, {}, { ...valid, revision: -1 }, { ...valid, revision: 1.5 }, { ...valid, language: "other" }, { ...valid, resolved_language: "ja" }, { ...valid, resolved_language: "ko" }, { ...valid, problem: "raw" }, { ...valid, widget_problem: "path" }, { ...valid, extra: true }]) expect(() => parseLanguage(value)).toThrow();
});
