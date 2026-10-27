// SPDX-License-Identifier: Apache-2.0
import { beforeEach, describe, expect, it, rs } from "@rstest/core";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { SignInPage } from "../src/account/account";
import { Field } from "../src/ui";
import {
  Locale,
  LanguageSelector,
  setLocale,
  getLocale,
  t,
  formatNumber,
  formatDecimal,
  enumLabel,
} from "../src/i18n";
import { loadCatalog } from "../src/catalogs";
import { enumMessages } from "../src/enum-messages";
// Synthetic catalogs exercise browser switching without generating product translations.
rs.mock("../src/catalogs", () => ({ loadCatalog: rs.fn() }));

const ko = {
  language: "언어",
  signInToAiprouter: "AIPRouter 로그인",
  unknown: "알 수 없음",
  [enumMessages.READY]: "준비됨",
};
const de = {
  language: "Sprache",
  signInToAiprouter: "Bei AIPRouter anmelden",
  unknown: "Unbekannt",
  [enumMessages.READY]: "Bereit",
};
beforeEach(async () => {
  rs.mocked(loadCatalog).mockImplementation(async (language) =>
    language === Locale.Korean
      ? ko
      : language === Locale.German
        ? de
        : undefined,
  );
  await setLocale(Locale.English);
  localStorage.clear();
});

function EditableForm() {
  const [value, setValue] = useState("");
  return (
    <Field label="Route name">
      <input value={value} onChange={(event) => setValue(event.target.value)} />
    </Field>
  );
}

describe("console localization", () => {
  it("translates sign-in and mounted controls without losing input or route state", async () => {
    window.history.replaceState(
      null,
      "",
      "/sign-in?return_to=%2Fkeys%3Forganization%3Dtest",
    );
    const address = window.location.href;
    render(
      <>
        <LanguageSelector />
        <SignInPage />
        <EditableForm />
      </>,
    );
    const input = screen.getByRole("textbox") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "my unsaved route" } });
    const select = screen.getByRole("combobox");
    await waitFor(() =>
      expect((select as HTMLSelectElement).disabled).toBe(false),
    );
    fireEvent.change(select, { target: { value: Locale.Korean } });
    await screen.findByRole("heading", { name: ko.signInToAiprouter });
    expect(document.documentElement.lang).toBe(Locale.Korean);
    expect(screen.getByRole("textbox")).toBe(input);
    expect(input.value).toBe("my unsaved route");
    expect(window.location.href).toBe(address);
    expect(localStorage.getItem("aiprouter.locale.v1")).toBe(Locale.Korean);
    expect(screen.getAllByRole("option")).toHaveLength(8);
    expect(screen.getByRole("combobox", { name: ko.language })).toBe(select);
  });

  it("restores a supported saved language", async () => {
    localStorage.setItem("aiprouter.locale.v1", Locale.German);
    render(
      <>
        <LanguageSelector />
        <SignInPage />
      </>,
    );
    await screen.findByRole("heading", { name: de.signInToAiprouter });
    expect(document.documentElement.lang).toBe(Locale.German);
  });

  it("defaults to English when storage is unavailable or its value is unknown", async () => {
    localStorage.setItem("aiprouter.locale.v1", "unsupported");
    const storage = rs
      .spyOn(Storage.prototype, "getItem")
      .mockImplementation(() => {
        throw new Error("denied");
      });
    rs.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied");
    });
    render(<LanguageSelector />);
    await waitFor(() =>
      expect((screen.getByRole("combobox") as HTMLSelectElement).disabled).toBe(
        false,
      ),
    );
    expect(getLocale()).toBe(Locale.English);
    storage.mockRestore();
    await setLocale(Locale.Korean);
    expect(getLocale()).toBe(Locale.Korean);
  });

  it("uses English for deferred languages while retaining the locale choice", async () => {
    await setLocale(Locale.Japanese);
    expect(getLocale()).toBe(Locale.Japanese);
    expect(t("language")).toBe("Language");
    expect(document.documentElement.lang).toBe(Locale.English);
  });

  it("keeps the current language and reports a failed catalog load", async () => {
    render(<LanguageSelector />);
    const select = screen.getByRole("combobox") as HTMLSelectElement;
    await waitFor(() => expect(select.disabled).toBe(false));
    rs.mocked(loadCatalog).mockRejectedValueOnce(
      new Error("chunk unavailable"),
    );
    fireEvent.change(select, { target: { value: Locale.Korean } });
    await screen.findByRole("alert");
    expect(select.value).toBe(Locale.English);
    expect(getLocale()).toBe(Locale.English);
  });

  it("ignores a stale language load after a newer choice", async () => {
    const earlier = setLocale(Locale.Japanese);
    await setLocale(Locale.English);
    await earlier;
    expect(getLocale()).toBe(Locale.English);
    expect(document.documentElement.lang).toBe(Locale.English);
  });

  it("preserves interpolation values as literal text", () => {
    expect(t("requestId", { value0: "$& <script>{value1}</script>" })).toBe(
      "Request ID: $& <script>{value1}</script>",
    );
  });

  it("formats precise counts, costs and enum labels in the selected locale", async () => {
    await setLocale(Locale.German);
    expect(formatNumber(12345678901234567890n)).toBe(
      "12.345.678.901.234.567.890",
    );
    expect(formatDecimal("12345678901234567890.001200")).toBe(
      "12.345.678.901.234.567.890,001200",
    );
    expect(formatNumber(undefined)).toBe(de.unknown);
    expect(enumLabel("READY")).toBe("Bereit");
    expect(enumLabel("UNRECOGNIZED_FUTURE_VALUE")).toBe(de.unknown);
  });
});
