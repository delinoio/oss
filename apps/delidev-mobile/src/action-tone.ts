// SPDX-License-Identifier: Apache-2.0

/**
 * Semantic presentation tone for mobile buttons. Danger marks buttons that
 * forget a profile or revoke authorization. It is presentation only and is
 * never derived from visible text. The desktop client uses the same
 * `data-action-tone` marker convention, but the two apps share no runtime code.
 */
export enum ActionTone {
  Default = "default",
  Danger = "danger",
}

/** Spread onto a native button to render the tone marker. */
export function actionTone(tone: ActionTone) {
  return { "data-action-tone": tone } as const;
}
