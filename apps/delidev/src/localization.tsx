// SPDX-License-Identifier: Apache-2.0
import { createInstance } from "i18next";
import { initReactI18next, useTranslation } from "react-i18next";
import { Fragment, useState, type Dispatch, type SetStateAction, type ReactElement } from "react";
import { english, korean } from "./locales/resources";

export enum SupportedLanguage { English = "en", Korean = "ko" }
type PluralBase<T> = T extends `${infer Name}_one` ? Name : never;
export type MessageKey = keyof typeof english | PluralBase<keyof typeof english>;
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

export interface OwnedMessage { readonly key: MessageKey; readonly values: Record<string, unknown> }
export function ownedMessage(key: MessageKey, values: Record<string, unknown> = {}): OwnedMessage {
  return { key, values };
}
export function resolveMessage(value: OwnedMessage | string | undefined): string | undefined {
  return typeof value === "object" ? copy(value.key, value.values) : value;
}

// Validation errors retain a stable catalog identity across language changes.
// Error.message remains English for diagnostics and existing technical callers.
export class ProductError extends Error {
  readonly productMessage: OwnedMessage;
  constructor(key: MessageKey) {
    super(english[key as keyof typeof english]);
    this.productMessage = ownedMessage(key);
  }
}
export function productError(error: unknown, fallback: MessageKey): OwnedMessage {
  return error instanceof ProductError ? error.productMessage : ownedMessage(fallback);
}

// Store presentation ownership rather than a translated result. Language changes
// update an existing notice without repeating the action that created it.
type MessageState<T> = string | OwnedMessage | (T extends string ? never : undefined);
export function useProductMessage<T extends string | undefined = undefined>(initial?: T): [T extends string ? string : string | undefined, Dispatch<SetStateAction<MessageState<T>>>] {
  useLocale();
  const [value, setValue] = useState<MessageState<T>>(initial as MessageState<T>);
  return [resolveMessage(value) as T extends string ? string : string | undefined, setValue];
}

/** Subscribe without making language part of business queries or lifetimes. */
export function useLocale(): SupportedLanguage {
  const { i18n: instance } = useTranslation();
  return instance.resolvedLanguage === SupportedLanguage.Korean ? SupportedLanguage.Korean : SupportedLanguage.English;
}

/** Only catalog-owned slots are parsed. Original data remains inert children.
 * Stable slot IDs preserve child identity when another language reorders them.
 */
export function LocalizedText({ id, components = {} }: {
  id: MessageKey; components?: Record<string, ReactElement>;
}) {
  useLocale();
  return copy(id).split(/(<s\d+\/>)/).map((part, index) => {
    const slot = /^<(s\d+)\/>$/.exec(part)?.[1];
    return slot ? <Fragment key={slot}>{components[slot]}</Fragment> : <Fragment key={`text-${index}`}>{part}</Fragment>;
  });
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

/** Localize an ISO observation without changing its original offset or fractional precision. */
export function formatTimestamp(value: string): string {
  const parts = /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2}:\d{2})(\.\d+)?(Z|[+-]\d{2}:\d{2})$/.exec(value);
  if (!parts || displayLocale() === "en-US") return value;
  const wall = new Date(`${parts[1]}T${parts[2]}Z`);
  if (!Number.isFinite(wall.getTime()) || wall.toISOString().slice(0, 19) !== `${parts[1]}T${parts[2]}` || !Number.isFinite(Date.parse(value))) return value;
  const date = new Intl.DateTimeFormat(displayLocale(), { timeZone: "UTC", year: "numeric", month: "long", day: "numeric" }).format(wall);
  return `${date} ${parts[2]}${parts[3] ?? ""} UTC${parts[4] === "Z" ? "" : parts[4]}`;
}

/** Group an exact decimal string without rounding, currency conversion or Number coercion. */
export function formatDecimal(value: string): string {
  if (value.length > 4096 || !/^-?(0|[1-9]\d*)(?:\.\d+)?$/.test(value)) return value;
  const [integer, fraction] = value.replace(/^-/, "").split(".");
  const formatter = new Intl.NumberFormat(displayLocale());
  const decimal = formatter.formatToParts(1.1).find(part => part.type === "decimal")?.value ?? ".";
  const sign = value.startsWith("-") ? formatter.formatToParts(-1).find(part => part.type === "minusSign")?.value ?? "-" : "";
  return sign + formatter.format(BigInt(integer)) + (fraction === undefined ? "" : decimal + fraction);
}
