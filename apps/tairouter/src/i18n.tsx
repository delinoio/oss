// SPDX-License-Identifier: Apache-2.0
import { useEffect, useState, useSyncExternalStore } from "react";
import english from "../i18n/messages/en.json";
import { enumMessages } from "./enum-messages";
import { loadCatalog } from "./catalogs";
import registry from "../i18n/locales.json";

export enum Locale {
  English = "en",
  Korean = "ko",
  Japanese = "ja",
  SimplifiedChinese = "zh-Hans",
  Spanish = "es",
  French = "fr",
  German = "de",
  BrazilianPortuguese = "pt-BR",
}
type MessageKey = keyof typeof english;
type Catalog = Record<MessageKey, string>;
const storageKey = "tairouter.locale.v1";
let locale = Locale.English;
let catalog: Partial<Catalog> = english;
let selection = 0;
const listeners = new Set<() => void>();
const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
};
export function getLocale() {
  return locale;
}
export function useLocale() {
  return useSyncExternalStore(subscribe, getLocale, () => Locale.English);
}
export function t(
  key: MessageKey,
  values: Record<string, unknown> = {},
): string {
  return (catalog[key] ?? english[key]).replace(
    /\{(value\d+)\}/g,
    (token, name: string) =>
      Object.hasOwn(values, name)
        ? typeof values[name] === "number" || typeof values[name] === "bigint"
          ? formatNumber(values[name] as number | bigint)
          : String(values[name] ?? "")
        : token,
  );
}
export async function setLocale(next: Locale) {
  if (!registry.locales.some(({ lang }) => lang === next)) return;
  const request = ++selection;
  const messages = next === Locale.English ? english : await loadCatalog(next);
  if (request !== selection) return;
  locale = next;
  catalog = messages ?? english;
  document.documentElement.lang = messages ? next : Locale.English;
  try {
    localStorage.setItem(storageKey, next);
  } catch {
    // Private browsing or storage policy can deny writes. Language selection still works for this page.
  }
  for (const listener of listeners) listener();
}
function savedLocale(): Locale {
  try {
    const saved = localStorage.getItem(storageKey);
    if (registry.locales.some(({ lang }) => lang === saved))
      return saved as Locale;
  } catch {
    // A browser storage policy must not prevent sign-in or console use.
  }
  return Locale.English;
}
export function LanguageSelector() {
  const current = useLocale();
  const [pending, setPending] = useState(false);
  const [failed, setFailed] = useState(false);
  async function select(next: Locale) {
    setPending(true);
    setFailed(false);
    try {
      await setLocale(next);
    } catch {
      setFailed(true);
    } finally {
      setPending(false);
    }
  }
  useEffect(() => {
    void select(savedLocale());
  }, []);
  return (
    <div className="language-toolbar">
      <label className="language-selector">
        <span>{t("language")}</span>
        <select
          value={current}
          disabled={pending}
          onChange={(event) => void select(event.target.value as Locale)}
        >
          {registry.locales.map(({ lang, label }) => (
            <option key={lang} value={lang} lang={lang}>
              {label}
            </option>
          ))}
        </select>
      </label>
      {pending && <span role="status">{t("loadingLanguage")}</span>}
      {failed && (
        <span role="alert">{t("languageCouldNotBeLoadedTryAgain")}</span>
      )}
    </div>
  );
}

export function enumLabel(value: string | undefined) {
  const key = value && enumMessages[value as keyof typeof enumMessages];
  return key ? t(key) : t("unknown");
}
export function formatNumber(value: number | bigint | undefined) {
  return value === undefined
    ? t("unknown")
    : new Intl.NumberFormat(getLocale()).format(value);
}
export function formatDecimal(value: string) {
  // Preserve arbitrary-precision decimal values from the management API. Converting to Number can round costs.
  if (!/^-?\d+(?:\.\d+)?$/.test(value)) return value;
  const [whole, fraction] = value.split(".");
  const decimal =
    new Intl.NumberFormat(getLocale())
      .formatToParts(1.1)
      .find((part) => part.type === "decimal")?.value ?? ".";
  const negativeZero =
    whole === "-0" ? new Intl.NumberFormat(getLocale()).format(-0) : undefined;
  return (
    (negativeZero ?? formatNumber(BigInt(whole))) +
    (fraction === undefined ? "" : decimal + fraction)
  );
}
