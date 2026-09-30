// SPDX-License-Identifier: Apache-2.0
export enum SubscriptionBrand { ChatGPT = "chatgpt", Claude = "claude", Grok = "grok" }
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
  { brand: SubscriptionBrand.ChatGPT, name: "ChatGPT", purpose: "For Codex", mark: "./subscription-marks/openai.svg", availability: SubscriptionAvailability.Planned },
  { brand: SubscriptionBrand.Claude, name: "Claude", purpose: "For Claude Code", mark: "./subscription-marks/claude.svg", availability: SubscriptionAvailability.Planned },
  { brand: SubscriptionBrand.Grok, name: "Grok", purpose: "For Grok Build", mark: "./subscription-marks/grok.svg", availability: SubscriptionAvailability.Planned },
];
