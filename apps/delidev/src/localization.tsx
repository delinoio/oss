// SPDX-License-Identifier: Apache-2.0
import { createInstance } from "i18next";
import { initReactI18next, Trans, useTranslation } from "react-i18next";
import type { ReactElement, ComponentType } from "react";
import { english, korean } from "./locales/resources";

export enum SupportedLanguage { English = "en", Korean = "ko" }
export type MessageKey = keyof typeof english;
export const i18n = createInstance();
void i18n.use(initReactI18next).init({
  resources: { en: { translation: english }, ko: { translation: korean } },
  lng: SupportedLanguage.English, fallbackLng: SupportedLanguage.English,
  supportedLngs: [SupportedLanguage.English, SupportedLanguage.Korean],
  keySeparator: false, initAsync: false, returnNull: false,
  interpolation: { escapeValue: false }, react: { useSuspense: false },
});

declare module "i18next" {
  interface CustomTypeOptions {
    defaultNS: "translation";
    // The wrapper below checks every key; avoid expanding thousands of keys in library overloads.
    resources: { translation: Record<string, string> };
    enableSelector: false;
    keySeparator: false;
  }
}

/** Pure presentation lookup. Callers retain stable IDs and source values. */
export function copy(key: MessageKey, values: Record<string, unknown> = {}): string {
  return i18n.t(key, values);
}

/** Subscribe without making language part of business queries or lifetimes. */
export function useLocale(): SupportedLanguage {
  const { i18n: instance } = useTranslation();
  return instance.resolvedLanguage === SupportedLanguage.Korean ? SupportedLanguage.Korean : SupportedLanguage.English;
}

const Translation = Trans as unknown as ComponentType<{ i18n: typeof i18n; i18nKey: MessageKey; values?: Record<string, unknown>; components?: Record<string, ReactElement> }>;

export function LocalizedText({ id, values, components }: {
  id: MessageKey; values?: Record<string, unknown>; components?: Record<string, ReactElement>;
}) {
  return <Translation i18n={i18n} i18nKey={id} values={values} components={components} />;
}

export function displayLocale(): string {
  return i18n.resolvedLanguage === SupportedLanguage.Korean ? "ko-KR" : "en-US";
}

export function formatNumber(value: number | bigint): string {
  return new Intl.NumberFormat(displayLocale()).format(value);
}

export function formatDate(value: Date, options?: Intl.DateTimeFormatOptions): string {
  return new Intl.DateTimeFormat(displayLocale(), options ?? { dateStyle: "medium", timeStyle: "medium" }).format(value);
}
