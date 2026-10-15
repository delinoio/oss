// SPDX-License-Identifier: Apache-2.0
import {
  SubscriptionLoginState,
  SubscriptionServiceId,
  type GrokDiagnostic,
} from "@delinoio/delidev-api-client";

/**
 * How a client treats one `GetSubscriptionProgress` response of its original
 * operation. The table in docs/cmds-delidev-grok-subscription-contract.md
 * ("Client classification of a progress response") lists every row, and
 * subscription-progress.test.ts asserts each one.
 */
export enum ProgressDisposition {
  /** The operation is still running: keep polling and drive the sign-in. */
  Active = "active",
  /**
   * The server reports a refusal that it deliberately leaves queued, so a later
   * claim can supersede it: keep polling and adopt whatever the server reports
   * next. Nothing about the sign-in is disposed.
   */
  Provisional = "provisional",
  /** The operation succeeded: verify it and move on to naming the account. */
  Success = "success",
  /** The operation reached a final outcome: stop polling and dispose. */
  Terminal = "terminal",
}

/**
 * Classifies a progress response by state alone, plus the one wire signal that
 * makes `unsupported` provisional. Only a Grok Take refused for an installation
 * problem is provisional: the server keeps that operation queued and attaches a
 * Grok diagnostic (field 10) that no other service ever populates. Every other
 * `unsupported` response, including a Codex or Claude one, is final. A state
 * outside the closed set is rejected rather than guessed.
 */
export function progressDisposition(
  state: SubscriptionLoginState,
  grokDiagnostic?: GrokDiagnostic,
): ProgressDisposition | undefined {
  switch (state) {
    case SubscriptionLoginState.PREPARING:
    case SubscriptionLoginState.WAITING:
      return ProgressDisposition.Active;
    case SubscriptionLoginState.SUCCEEDED:
      return ProgressDisposition.Success;
    case SubscriptionLoginState.UNSUPPORTED:
      return grokDiagnostic
        ? ProgressDisposition.Provisional
        : ProgressDisposition.Terminal;
    case SubscriptionLoginState.CANCELED:
    case SubscriptionLoginState.EXPIRED:
    case SubscriptionLoginState.RECOVERY_REQUIRED:
    case SubscriptionLoginState.FAILED:
      return ProgressDisposition.Terminal;
    default:
      return undefined;
  }
}

/**
 * Only ChatGPT sign-in uses the original browser URL and callback receiver.
 * Grok progress never carries a URL or user code, so observing a claimed Grok
 * operation must not enter the browser path.
 */
export function drivesBrowserLogin(service: SubscriptionServiceId) {
  return service === SubscriptionServiceId.ChatGPT;
}
