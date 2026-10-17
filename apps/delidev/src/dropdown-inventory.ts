// SPDX-License-Identifier: Apache-2.0

/**
 * How a DeliDev dropdown-like surface satisfies the dropdown layout invariance
 * contract: opening, filtering or dismissing it must leave every unrelated
 * rectangle and scroll metric unchanged. The desktop contract carries the same
 * inventory as a table; `dropdown-inventory.test.ts` keeps source, this list and
 * the contract together.
 */
export enum DropdownTreatment {
  /** Presented by `useAnchoredPopup` (top layer, `popover="manual"`). */
  AnchoredPopup = "anchored-popup",
  /** ScrollPicker's own equivalent lifecycle (`picker-overlay.ts`). */
  ScrollPicker = "scroll-picker",
  /** A surface-specific `popover="manual"` top-layer popup that was already out of flow. */
  TopLayerPopover = "top-layer-popover",
  /** The menu is the content of a modal dialog, not a dropdown over the page. */
  ModalDialog = "modal-dialog",
  /** Results render in a region whose height is reserved before opening. */
  ReservedRegion = "reserved-region",
  /** A persistent expand/collapse region the user asked for, not a transient dropdown. */
  PersistentDisclosure = "persistent-disclosure",
  /** An in-flow menu kept on purpose; the reason names the constraint. */
  Exception = "exception",
}

export interface DropdownInventoryEntry {
  /** Source module under `src/` that owns the surface or its shared presentation. */
  file: string;
  /** Stable family description used by the contract table. */
  family: string;
  treatment: DropdownTreatment;
  /** Why the treatment is correct, or the geometry check that proves it. */
  evidence: string;
}

/** Every module that renders a listbox, menu, combobox, popover or details. */
export const dropdownInventory: readonly DropdownInventoryEntry[] = [
  {
    file: "language.tsx",
    family: "Appearance language combobox",
    treatment: DropdownTreatment.AnchoredPopup,
    evidence:
      "scripts/test-settings-layout.mjs (DELIDEV_LAYOUT_LANGUAGE_ONLY=1)",
  },
  {
    file: "reasoning-effort-field.tsx",
    family: "Reasoning effort and Subagent effort suggestions",
    treatment: DropdownTreatment.AnchoredPopup,
    evidence: "scripts/test-reasoning-effort-layout.mjs",
  },
  {
    file: "queue.tsx",
    family: "Queued-input More menu",
    treatment: DropdownTreatment.AnchoredPopup,
    evidence: "scripts/test-queue-layout.mjs",
  },
  {
    file: "session-diff.tsx",
    family: "Session Diff options popup",
    treatment: DropdownTreatment.AnchoredPopup,
    evidence: "scripts/test-dropdown-layout.mjs (session-diff-options)",
  },
  {
    file: "details-action-menu.tsx",
    family: "Saved-server row menu (shared details/summary action menu)",
    treatment: DropdownTreatment.AnchoredPopup,
    evidence: "scripts/test-dropdown-layout.mjs (saved-connection-row-menu)",
  },
  {
    file: "api-entry-row.tsx",
    family: "API key row More actions",
    treatment: DropdownTreatment.AnchoredPopup,
    evidence: "scripts/test-dropdown-layout.mjs (api-entry-more-panel)",
  },
  {
    file: "subscription-settings.tsx",
    family: "Subscription row More actions",
    treatment: DropdownTreatment.AnchoredPopup,
    evidence: "scripts/test-dropdown-layout.mjs (subscription-more-panel)",
  },
  {
    file: "scroll-picker.tsx",
    family: "ScrollPicker searchable selectors",
    treatment: DropdownTreatment.ScrollPicker,
    evidence: "popover=manual with picker-overlay.ts geometry; unchanged",
  },
  {
    file: "starting-branches.tsx",
    family: "Starting branch selector",
    treatment: DropdownTreatment.TopLayerPopover,
    evidence: "fixed manual popup; unchanged",
  },
  {
    file: "sidebar-more.tsx",
    family: "Sidebar More menu",
    treatment: DropdownTreatment.TopLayerPopover,
    evidence: "popover=manual; unchanged",
  },
  {
    file: "session-row-actions.tsx",
    family: "Session row actions menu",
    treatment: DropdownTreatment.TopLayerPopover,
    evidence: "popover=manual; unchanged",
  },
  {
    file: "project-settings-menu.tsx",
    family: "Project settings menu",
    treatment: DropdownTreatment.TopLayerPopover,
    evidence: "popover=manual; unchanged",
  },
  {
    file: "skill-completion.tsx",
    family: "Skill completion list",
    treatment: DropdownTreatment.TopLayerPopover,
    evidence: "popover=manual; unchanged",
  },
  {
    file: "pull-requests.tsx",
    family: "Pull request repository details",
    treatment: DropdownTreatment.TopLayerPopover,
    evidence: "popover=manual; unchanged",
  },
  {
    file: "subscription-paid-credits.tsx",
    family: "Paid credit exact-value tooltip",
    treatment: DropdownTreatment.TopLayerPopover,
    evidence: "popover=manual; scripts/test-subscription-details-layout.mjs",
  },
  {
    file: "session-hover-card.tsx",
    family: "Session hover card",
    treatment: DropdownTreatment.TopLayerPopover,
    evidence: "popover=manual; scripts/test-session-hover-layout.mjs",
  },
  {
    file: "subscription-rail.tsx",
    family: "Subscription rail account and problem popovers",
    treatment: DropdownTreatment.TopLayerPopover,
    evidence: "popover=manual portals; unchanged",
  },
  {
    file: "session-tool-menu.tsx",
    family: "Session tool menu",
    treatment: DropdownTreatment.ModalDialog,
    evidence: "role=menu is the body of a showModal dialog",
  },
  {
    file: "agent-worker-source-wizard.tsx",
    family: "Worker model discovery results",
    treatment: DropdownTreatment.ReservedRegion,
    evidence: "reserved 280px region; pnpm test:worker-model-layout",
  },
  {
    file: "disclosure.tsx",
    family: "Persistent disclosure",
    treatment: DropdownTreatment.PersistentDisclosure,
    evidence: "user-requested expansion of persistent content; not a dropdown",
  },
  {
    file: "session-browser.tsx",
    family: "Session Browser overflow menu",
    treatment: DropdownTreatment.Exception,
    evidence:
      "The address row sits directly above a native browser view that web top-layer content cannot cover, so the menu stays in flow and wraps to its own row",
  },
];
