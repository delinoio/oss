// SPDX-License-Identifier: Apache-2.0
import { object, text } from "./documents";
import type { MessageKey } from "./localization";

/**
 * Why the desktop does or does not authorize a new Session terminal gesture.
 * The server still enforces its own ready-workspace rule; this only keeps the
 * frontend from admitting a gesture that the server must reject.
 */
export enum TerminalOpenEligibility {
  Eligible = "eligible",
  /** A read-only Sidechat parent snapshot never owns terminals. */
  SidechatBlocked = "sidechat-blocked",
  /** Workspace preparation is missing or not in the ready state. */
  PreparationNotReady = "preparation-not-ready",
}

/** Preparation state value that authorizes terminal creation. */
const readyPreparationState = "ready";

/** Whether the authenticated Session document reports a ready workspace. */
export const workspacePreparationReady = (
  data: Record<string, unknown>,
): boolean => text(object(data.preparation).state) === readyPreparationState;

/**
 * Derives the single terminal eligibility rule from the authenticated Session
 * document. Sidechat wins so its specific reason stays distinct.
 */
export const terminalOpenEligibility = (
  data: Record<string, unknown>,
): TerminalOpenEligibility =>
  object(data.fork).sidechat_parent_snapshot
    ? TerminalOpenEligibility.SidechatBlocked
    : workspacePreparationReady(data)
      ? TerminalOpenEligibility.Eligible
      : TerminalOpenEligibility.PreparationNotReady;

/** Localized shortcut Help reason for each blocked eligibility. */
export const terminalUnavailableReason = (
  eligibility: TerminalOpenEligibility,
): MessageKey =>
  eligibility === TerminalOpenEligibility.PreparationNotReady
    ? "shortcuts.terminalsPreparing"
    : "shortcuts.terminalsUnavailable";
