import { copy } from "./localization";
// SPDX-License-Identifier: Apache-2.0
export enum SubscriptionBrand { ChatGPT = "chatgpt", Claude = "claude", Grok = "grok", OpenCodeGo = "opencode_go" }
export enum SubscriptionAvailability { Planned = "planned" }

export interface SubscriptionProviderDescriptor {
  brand: SubscriptionBrand;
  name: string;
  purpose: string;
  mark: string;
  availability: SubscriptionAvailability;
}

// Branding is presentation metadata, never an authority for native operations.
// Native profile/capability adapters must be verified before extending availability.
export const subscriptionCatalog: readonly SubscriptionProviderDescriptor[] = [
  { brand: SubscriptionBrand.ChatGPT, name: "ChatGPT", get purpose() { return copy("subscription-catalog.extra.aa86b17a0399"); }, mark: "./subscription-marks/openai.svg", availability: SubscriptionAvailability.Planned },
  { brand: SubscriptionBrand.Claude, name: "Claude", get purpose() { return copy("subscription-catalog.extra.640e63f6b085"); }, mark: "./subscription-marks/claude.svg", availability: SubscriptionAvailability.Planned },
  { brand: SubscriptionBrand.Grok, name: "Grok", get purpose() { return copy("subscription-catalog.extra.edb5295b4f61"); }, mark: "./subscription-marks/grok.svg", availability: SubscriptionAvailability.Planned },
  { brand: SubscriptionBrand.OpenCodeGo, name: "OpenCode Go", get purpose() { return copy("subscription-catalog.openCodePurpose"); }, mark: "./harness-marks/opencode-light.svg", availability: SubscriptionAvailability.Planned },
];
