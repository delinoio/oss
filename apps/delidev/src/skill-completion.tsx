// SPDX-License-Identifier: Apache-2.0
import {
  useEffect,
  useLayoutEffect,
  useMemo,
  useId,
  useRef,
  useState,
  useCallback,
  type ClipboardEvent,
  type KeyboardEvent,
  type MouseEvent,
  type ReactNode,
  type RefObject,
} from "react";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import {
  SkillQuery,
  SkillProvenance,
  isEntityId,
  type SkillEntry,
  type SkillSelection,
} from "@delinoio/delidev-api-client";
import { useAppearancePreferences } from "./appearance";
import {
  ExternalLinkFailure,
  externalLinkFailure,
  externalLinkFailureMessages,
  externalLinksAvailable,
  openExternalUrl,
} from "./external-links";
import "./external-links.css";
import {
  PromptEditHistory,
  PromptEditKind,
  type PromptSelection,
  type PromptSnapshot,
} from "./prompt-edit-history";
import {
  buildLayout,
  type FrozenCollapse,
  canonicalRangeForDisplay,
  editFrozenCollapse,
  freezeCollapse,
  convertBareUrls,
  convertTypedUrl,
  diffDisplay,
  linkAtCaret,
  revealedStarts,
  sameStarts,
  scanDraft,
  scanMarkdown,
  replacedRange,
  shiftStarts,
  type PromptLayout,
  type ScannedDraft,
  type TextRange,
} from "./prompt-markdown-links";
import { ShortcutPlatform, shortcutPlatform } from "./shortcuts";
import { copy, useLocale } from "./localization";
import {
  skillCompletionAnchorUsable,
  useSkillCompletionOverlay,
} from "./skill-completion-overlay";

/** Canonical prompt limit in UTF-8 bytes; owners enforce the same value. */
export const MAX_PROMPT_BYTES = 256 << 10;
const promptBytes = (text: string) => new TextEncoder().encode(text).byteLength;
enum HistoryAction {
  Undo = "undo",
  Redo = "redo",
}
function historyAction(event: {
  key: string;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
}): HistoryAction | undefined {
  const mac = shortcutPlatform() === ShortcutPlatform.Mac;
  const primary = mac
    ? event.metaKey && !event.ctrlKey
    : event.ctrlKey && !event.metaKey;
  if (!primary || event.altKey) return;
  const key = event.key.toLowerCase();
  if (key === "z")
    return event.shiftKey ? HistoryAction.Redo : HistoryAction.Undo;
  if (key === "y" && !mac && !event.shiftKey) return HistoryAction.Redo;
}
const selectionDirection = (input: HTMLTextAreaElement) =>
  input.selectionDirection === "backward" ||
  input.selectionDirection === "forward"
    ? input.selectionDirection
    : "none";

export interface SkillToken {
  start: number;
  end: number;
  prefix: string;
}
export function skillToken(
  value: string,
  caret: number,
): SkillToken | undefined {
  const before = value.slice(0, caret),
    match = /(?:^|\s)\$([^\s$]*)$/u.exec(before);
  if (!match) return undefined;
  const suffix = /^[^\s$]*/u.exec(value.slice(caret))![0];
  return {
    start: caret - match[1]!.length - 1,
    end: caret + suffix.length,
    prefix: match[1]!,
  };
}
export enum SkillAvailability {
  Available = "available",
  Unavailable = "unavailable",
  Unknown = "unknown",
}
// Presentation identity of an inventory row, excluding per-read opaque IDs.
function skillFingerprint(entry: SkillEntry): string {
  return JSON.stringify([
    entry.name,
    entry.provenance,
    entry.selection!.contentRevision,
  ]);
}
const SKILL_RANGE_PATTERN = /(?:^|\s)\$([^\s$]+)(?=$|\s)/gu;
// Lazily walks complete whitespace-delimited tokens so large drafts never
// materialize an array of every occurrence.
function* iterateSkillRanges(value: string): Generator<SkillToken> {
  for (const match of value.matchAll(SKILL_RANGE_PATTERN))
    yield {
      start: match.index! + match[0].length - match[1]!.length - 1,
      end: match.index! + match[0].length,
      prefix: match[1]!,
    };
}
export function skillRanges(value: string): SkillToken[] {
  return [...iterateSkillRanges(value)];
}
function hasSkillRange(value: string): boolean {
  return iterateSkillRanges(value).next().done === false;
}
// Painted overlay budget: two spans per range keeps the mirror within 512 spans.
export const MAX_PAINTED_SKILL_RANGES = 256;
export const MAX_SELECTED_SKILL_RANGES = 16;
export interface SkillTokenAnalysis {
  // Exact number of confirmed-absent complete tokens, independent of painting.
  unavailableCount: number;
  // Painted absent ranges; empty when the exact count exceeds the paint budget.
  unavailable: SkillToken[];
  // Painted explicit selections that are not confirmed absent.
  selected: SkillToken[];
}
export interface SkillTokenAnalysisInput {
  // Exact current inventory names; undefined when absence cannot be confirmed.
  names?: ReadonlySet<string>;
  // The caret-owned token start and whether its prefix matches any inventory name.
  caret?: { start: number; prefixAvailable: boolean };
  // The valid retained explicit selection for a complete token, if any.
  bound?: (range: SkillToken) => SkillSelection | undefined;
  // Exact four-field selections in the current inventory; undefined when unknown.
  selectionKeys?: ReadonlySet<string>;
}
// The backend requires all four selection fields to match exactly.
export function skillSelectionKey(selection: SkillSelection): string {
  return [
    selection.workerDeviceId,
    selection.inventoryId,
    selection.skillId,
    selection.contentRevision,
  ].join("|");
}
// One lazy pass: one Set lookup per complete token, an exact absence count and
// at most MAX_PAINTED_SKILL_RANGES materialized ranges.
export function analyzeSkillTokens(
  value: string,
  { names, caret, bound, selectionKeys }: SkillTokenAnalysisInput,
): SkillTokenAnalysis {
  const unavailable: SkillToken[] = [],
    selected: SkillToken[] = [];
  let unavailableCount = 0;
  if (names || bound) {
    for (const range of iterateSkillRanges(value)) {
      const selection = bound?.(range);
      if (
        (names &&
          !names.has(range.prefix) &&
          !(caret && range.start === caret.start && caret.prefixAvailable)) ||
        // A same-named replacement cannot satisfy an explicit binding whose
        // exact selection is absent from the confirmed inventory.
        (selection &&
          selectionKeys &&
          !selectionKeys.has(skillSelectionKey(selection)))
      ) {
        unavailableCount++;
        if (unavailable.length < MAX_PAINTED_SKILL_RANGES)
          unavailable.push(range);
        continue;
      }
      if (selection && selected.length < MAX_SELECTED_SKILL_RANGES)
        selected.push(range);
      if (!names && selected.length === MAX_SELECTED_SKILL_RANGES) break;
    }
  }
  // Beyond the budget the native text stays untouched; the count alone reports it.
  if (unavailableCount > MAX_PAINTED_SKILL_RANGES)
    return { unavailableCount, unavailable: [], selected: [] };
  return {
    unavailableCount,
    unavailable,
    selected: selected.slice(0, MAX_PAINTED_SKILL_RANGES - unavailable.length),
  };
}
// A malformed or mixed snapshot cannot prove absence in the current scope.
function completeInventory(entries: readonly SkillEntry[]): boolean {
  const ids = new Set<string>(),
    first = entries[0]?.selection;
  return (
    entries.length <= 256 &&
    entries.every((entry) => {
      const selection = entry.selection;
      if (
        !entry.name ||
        /[\s$]/u.test(entry.name) ||
        !selection ||
        !isEntityId(selection.skillId) ||
        !isEntityId(selection.inventoryId) ||
        !isEntityId(selection.workerDeviceId) ||
        !/^[a-f0-9]{64}$/.test(selection.contentRevision) ||
        ids.has(selection.skillId) ||
        selection.inventoryId !== first?.inventoryId ||
        selection.workerDeviceId !== first?.workerDeviceId
      )
        return false;
      ids.add(selection.skillId);
      return true;
    })
  );
}
export interface SkillTokenBinding {
  start: number;
  end: number;
  token: string;
  selection: SkillSelection;
  stale: boolean;
  context?: string;
  ambiguous?: boolean;
}
export function editedBindings(
  before: string,
  after: string,
  bindings: readonly SkillTokenBinding[],
): SkillTokenBinding[] {
  let start = 0;
  while (
    start < before.length &&
    start < after.length &&
    before[start] === after[start]
  )
    start++;
  let end = before.length,
    nextEnd = after.length;
  while (
    end > start &&
    nextEnd > start &&
    before[end - 1] === after[nextEnd - 1]
  ) {
    end--;
    nextEnd--;
  }
  const delta = after.length - before.length;
  return bindings.flatMap((binding) => {
    if (binding.ambiguous) return [binding];
    if (
      (end > binding.start && start < binding.end) ||
      (start > binding.start && start < binding.end)
    )
      return [];
    const shift = start <= binding.start ? delta : 0,
      next = {
        ...binding,
        start: binding.start + shift,
        end: binding.end + shift,
      };
    return after.slice(next.start, next.end) === next.token &&
      (next.start === 0 || /\s/u.test(after[next.start - 1]!)) &&
      (next.end === after.length || /\s/u.test(after[next.end]!))
      ? [next]
      : [];
  });
}
export interface SkillCompletionEdit {
  // Complete draft after the completion, including any inserted separator.
  next: string;
  // Literal `$name` bytes that the selected binding covers.
  replacement: string;
  // Exclusive end of `replacement`; the separator is never part of the binding.
  end: number;
  // Collapsed caret offset: past the separator, never inside the token.
  caret: number;
}
// A completion adds one ASCII space unless the preserved suffix already starts
// with whitespace. Existing whitespace of any kind (space, tab, newline) is
// reused byte for byte, so the suffix is never rewritten.
export function skillCompletionSeparator(suffix: string): string {
  return /^\s/u.test(suffix) ? "" : " ";
}
// Pure text transaction for one accepted completion. It replaces the full
// token range (caret-inside partial tokens included), preserves the prefix and
// suffix exactly, and leaves the caret one UTF-16 unit past the token: just
// after the inserted space, or just after the first existing whitespace unit.
export function skillCompletionEdit(
  value: string,
  token: SkillToken,
  name: string,
): SkillCompletionEdit {
  const replacement = `$${name}`,
    suffix = value.slice(token.end),
    end = token.start + replacement.length;
  return {
    next:
      value.slice(0, token.start) +
      replacement +
      skillCompletionSeparator(suffix) +
      suffix,
    replacement,
    end,
    caret: end + 1,
  };
}
export function useSkillCompletion({
  value,
  change,
  textarea,
  machineId,
  agentId,
  sessionId = "",
  projectId = "",
  active = true,
  enabled = true,
  disabled = false,
  initialBindings = [],
  bindingsChanged,
  retainTransportContext = false,
}: {
  value: string;
  change: (value: string, bindings?: SkillTokenBinding[]) => boolean | void;
  textarea: RefObject<HTMLTextAreaElement | null>;
  machineId: string;
  agentId: string;
  sessionId?: string;
  projectId?: string;
  active?: boolean;
  enabled?: boolean;
  disabled?: boolean;
  initialBindings?: SkillTokenBinding[];
  bindingsChanged?: (bindings: SkillTokenBinding[]) => void;
  retainTransportContext?: boolean;
}) {
  const locale = useLocale();
  const transport = useTransport(),
    id = useId();
  const scope = `${machineId}:${agentId}:${sessionId}:${projectId}`;
  const previousScope = useRef({
    scope: initialBindings.find((binding) => binding.context)?.context ?? scope,
    transport,
  });
  const [bindings, setBindings] =
      useState<SkillTokenBinding[]>(initialBindings),
    [caret, setCaret] = useState(0),
    [dismissed, setDismissed] = useState(false),
    [selected, setSelected] = useState(0);
  useEffect(() => {
    bindingsChanged?.(bindings);
  }, [bindings, bindingsChanged]);
  const composing = useRef(false),
    editable = useRef(false);
  const [composition, setComposition] = useState(false);
  // Canonical Markdown stays the draft. The textarea shows a collapsed display
  // text derived from it, so selection, bindings and history map through here.
  const [revealed, setRevealed] = useState<number[]>([]);
  const [linkFailure, setLinkFailure] = useState<ExternalLinkFailure>();
  const revealedRef = useRef(revealed);
  revealedRef.current = revealed;
  const selectionRef = useRef<PromptSelection>({
    start: 0,
    end: 0,
    direction: "none",
  });
  const pendingSelection = useRef(false);
  // Collapse state captured when composition starts and updated by every
  // composed edit; the layout is derived from it, never from a re-parse of the
  // draft, until composition ends.
  const compositionCollapse = useRef<FrozenCollapse | undefined>(undefined);
  // The one scan taken when composition ends. The revealed set and the layout
  // are both derived from it, so the final draft is parsed exactly once.
  const settledScan = useRef<ScannedDraft | undefined>(undefined);
  const history = useRef(new PromptEditHistory<SkillTokenBinding>());
  const linkOpening = useRef(false);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const completion = useRef<HTMLDivElement>(null),
    keyboardNavigation = useRef(false);
  const dismissCompletion = useCallback(() => setDismissed(true), []);
  editable.current = active && !disabled;
  const canEdit = () =>
    editable.current && skillCompletionAnchorUsable(textarea.current);
  // The caret token describes where the user is typing and is independent of
  // panel dismissal; only the panel, query and keyboard ownership honor dismissal.
  const caretToken = !composing.current ? skillToken(value, caret) : undefined;
  const token = !dismissed ? caretToken : undefined;
  const contextChanged =
    previousScope.current.scope !== scope ||
    (!retainTransportContext && previousScope.current.transport !== transport);
  useEffect(() => {
    if (contextChanged && machineId && agentId) {
      // Opaque selections belong to one transport. History compares only text
      // and scope, so an equal-text snapshot from the previous transport could
      // otherwise restore a binding that looks current.
      if (previousScope.current.transport !== transport)
        history.current.reset();
      previousScope.current = { scope, transport };
      setBindings((prior) =>
        prior.map((binding) => ({ ...binding, stale: true })),
      );
      setSelected(0);
    }
  }, [scope, transport, contextChanged, machineId, agentId]);
  // Reveal, collapse, undo and conversion move the display text under the
  // caret, so the canonical selection is re-applied after that render.
  useLayoutEffect(() => {
    if (!pendingSelection.current) return;
    pendingSelection.current = false;
    const input = textarea.current;
    if (!input || composing.current) return;
    const selection = selectionRef.current,
      start = layout.toDisplay(Math.min(selection.start, value.length)),
      end = layout.toDisplay(Math.min(selection.end, value.length));
    if (input.selectionStart !== start || input.selectionEnd !== end)
      input.setSelectionRange(start, end, selection.direction);
  });
  const hasRanges = hasSkillRange(value);
  const query = useQuery(
    SkillQuery.listSkills,
    { machineId, agentId, sessionId, projectId },
    {
      enabled:
        active &&
        enabled &&
        !disabled &&
        (!!token || hasRanges) &&
        !!machineId &&
        !!agentId &&
        !contextChanged,
      retry: false,
      staleTime: 0,
      gcTime: 0,
    },
  );
  const prefix = token?.prefix.toLocaleLowerCase() ?? "";
  const inventoryKnown =
    active &&
    enabled &&
    !disabled &&
    !!machineId &&
    !!agentId &&
    !contextChanged &&
    query.isSuccess &&
    !query.isFetching &&
    !query.error &&
    completeInventory(query.data.skills);
  const inventory = inventoryKnown ? query.data!.skills : [];
  const visible = active && !disabled && Boolean(token);
  useSkillCompletionOverlay(completion, textarea, visible, dismissCompletion);
  const retained = useRef<{
    scope: string;
    transport: typeof transport;
    entries: SkillEntry[];
  }>({ scope, transport, entries: [] });
  if (
    !visible ||
    retained.current.scope !== scope ||
    retained.current.transport !== transport
  )
    retained.current = { scope, transport, entries: [] };
  const live = inventory.filter(
    (entry) =>
      entry.selection && entry.name.toLocaleLowerCase().startsWith(prefix),
  );
  if (visible && inventoryKnown) {
    // SkillIDs and InventoryIDs are minted per read, so reconcile retained rows
    // by a presentation-only fingerprint and match equal fingerprints by count.
    // Acceptance still uses only the current opaque selection.
    const remaining = new Map<string, number>();
    for (const entry of inventory) {
      const key = skillFingerprint(entry);
      remaining.set(key, (remaining.get(key) ?? 0) + 1);
    }
    const unmatched = retained.current.entries.filter((entry) => {
      const key = skillFingerprint(entry),
        count = remaining.get(key) ?? 0;
      if (count <= 0) return true;
      remaining.set(key, count - 1);
      return false;
    });
    retained.current.entries = [...live, ...unmatched].slice(0, 256);
  }
  const liveEntries = new Set<SkillEntry>(inventory);
  const candidates = (inventoryKnown ? retained.current.entries : [])
    .filter((entry) => entry.name.toLocaleLowerCase().startsWith(prefix))
    .map((entry) => ({
      ...entry,
      availability: liveEntries.has(entry)
        ? SkillAvailability.Available
        : SkillAvailability.Unavailable,
    }))
    .sort(
      (a, b) =>
        Number(b.name.toLocaleLowerCase() === prefix) -
          Number(a.name.toLocaleLowerCase() === prefix) ||
        a.name.localeCompare(b.name) ||
        Number(b.availability === SkillAvailability.Available) -
          Number(a.availability === SkillAvailability.Available) ||
        a.selection!.skillId.localeCompare(b.selection!.skillId),
    );
  const enabledIndices = candidates.flatMap((entry, index) =>
    entry.availability === SkillAvailability.Available ? [index] : [],
  );
  const validIndex = enabledIndices.includes(selected)
    ? selected
    : (enabledIndices[0] ?? -1);
  // Unavailable presentation describes confirmed absence, while selected styling
  // describes retained explicit authority. Unknown reads and pending locks must
  // not invent or erase either, so both come from one budgeted lazy pass.
  const skills = inventoryKnown ? query.data!.skills : undefined,
    scanSuppressed = composing.current || composition,
    caretStart = caretToken?.start,
    caretPrefix = caretToken?.prefix;
  const analysis = useMemo(() => {
    if (scanSuppressed) return analyzeSkillTokens("", {});
    const selectionKeys = skills
      ? new Set(skills.map((entry) => skillSelectionKey(entry.selection!)))
      : undefined;
    const names = skills
      ? new Set(skills.map((entry) => entry.name))
      : undefined;
    const caretLower = caretPrefix?.toLocaleLowerCase();
    const caret =
      skills && caretStart !== undefined && caretLower !== undefined
        ? {
            start: caretStart,
            prefixAvailable: skills.some((entry) =>
              entry.name.toLocaleLowerCase().startsWith(caretLower),
            ),
          }
        : undefined;
    let bound: SkillTokenAnalysisInput["bound"];
    if (
      bindings.length &&
      !contextChanged &&
      isEntityId(machineId) &&
      isEntityId(agentId)
    ) {
      const bindingsByRange = new Map<string, SkillTokenBinding[]>();
      for (const binding of bindings) {
        const key = `${binding.start}:${binding.end}:${binding.token}`,
          matches = bindingsByRange.get(key);
        if (matches) matches.push(binding);
        else bindingsByRange.set(key, [binding]);
      }
      bound = (range) => {
        const matches = bindingsByRange.get(
            `${range.start}:${range.end}:$${range.prefix}`,
          ),
          binding = matches?.[0];
        return matches?.length !== 1 ||
          !binding ||
          binding.stale ||
          binding.ambiguous ||
          (binding.context && binding.context !== scope) ||
          !isEntityId(binding.selection.skillId) ||
          !isEntityId(binding.selection.inventoryId) ||
          !isEntityId(binding.selection.workerDeviceId) ||
          !/^[a-f0-9]{64}$/.test(binding.selection.contentRevision)
          ? undefined
          : binding.selection;
      };
    }
    return analyzeSkillTokens(value, { names, caret, bound, selectionKeys });
  }, [
    value,
    skills,
    scanSuppressed,
    caretStart,
    caretPrefix,
    bindings,
    contextChanged,
    machineId,
    agentId,
    scope,
  ]);
  const { unavailable, unavailableCount, selected: selectedRanges } = analysis;
  // Links and skill ranges share one painted-range budget. Skill ranges are
  // already bounded by the analysis, so links yield: when they no longer fit in
  // the remainder, the whole draft is shown as plain source (no partial
  // collapse) while the exact unavailable count stays reported. An IME owns the
  // textarea text and selection while composing, so no display state may be
  // re-derived then: composition empties the skill analysis (growing the
  // remainder) and the link count changes with every keystroke. The collapse
  // state captured at composition start is therefore held as a whole, and the
  // budget is recomputed only once composition ends.
  const linkBudget =
    MAX_PAINTED_SKILL_RANGES - unavailable.length - selectedRanges.length;
  const frozenCollapse = scanSuppressed
    ? compositionCollapse.current
    : undefined;
  const layout = useMemo(
    () =>
      buildLayout(
        value,
        revealed,
        linkBudget,
        frozenCollapse,
        settledScan.current,
      ),
    [value, revealed, linkBudget, frozenCollapse],
  );
  const layoutRef = useRef<PromptLayout>(layout);
  layoutRef.current = layout;
  // Crossing the budget swaps the whole display text under the caret, so the
  // canonical selection is re-applied after that render.
  const collapseSuppressed = useRef(layout.collapseSuppressed);
  if (collapseSuppressed.current !== layout.collapseSuppressed) {
    collapseSuppressed.current = layout.collapseSuppressed;
    pendingSelection.current = true;
  }
  const confirmedCount = useRef<{
    scope: string;
    transport: typeof transport;
    count?: number;
  }>({ scope, transport });
  const announcementLocale = useRef(locale);
  const [announcement, setAnnouncement] = useState<{
    scope: string;
    transport: typeof transport;
    locale: typeof locale;
    count: number;
  }>();
  useEffect(() => {
    if (announcementLocale.current !== locale) {
      announcementLocale.current = locale;
      // Retire the historical live message instead of translating it in place.
      setAnnouncement(undefined);
    }
    if (
      confirmedCount.current.scope !== scope ||
      confirmedCount.current.transport !== transport
    ) {
      confirmedCount.current = { scope, transport };
      setAnnouncement(undefined);
    }
    if (!inventoryKnown || composition) {
      // Unknown is not zero. Retain the last confirmed count only for deduping,
      // so refetching or IME cannot replay an unchanged absence announcement.
      setAnnouncement(undefined);
      return;
    }
    const count = unavailableCount,
      previous = confirmedCount.current.count;
    if (previous === count) return;
    confirmedCount.current.count = count;
    // Do not announce an initial zero; a confirmed recovery from absence does
    // need a localized update even when the completion panel is dismissed.
    if (count > 0 || (previous !== undefined && previous > 0))
      setAnnouncement({ scope, transport, locale, count });
  }, [scope, transport, locale, inventoryKnown, composition, unavailableCount]);
  const announcedCount =
    inventoryKnown &&
    !composition &&
    announcementLocale.current === locale &&
    confirmedCount.current.scope === scope &&
    confirmedCount.current.transport === transport &&
    announcement?.scope === scope &&
    announcement.transport === transport &&
    announcement.locale === locale
      ? announcement.count
      : undefined;
  const snapshot = (): PromptSnapshot<SkillTokenBinding> => ({
    value,
    bindings,
    selection: selectionRef.current,
  });
  // One canonical transaction: owner draft, bindings, history, selection and
  // revealed links change together so undo restores them as a single step.
  const commit = (
    next: string,
    nextBindings: SkillTokenBinding[],
    selection: PromptSelection,
    kind: PromptEditKind,
    coalesce: boolean,
    replaced?: TextRange,
    insertedLength = 0,
    frozen?: FrozenCollapse,
  ): boolean => {
    if (!canEdit() || change(next, nextBindings) === false) return false;
    history.current.record(
      snapshot(),
      { value: next, bindings: nextBindings, selection },
      kind,
      coalesce,
    );
    setBindings(nextBindings);
    selectionRef.current = selection;
    setCaret(selection.start);
    if (frozen) compositionCollapse.current = frozen;
    setRevealed(
      composing.current && replaced
        ? shiftStarts(revealedRef.current, replaced, insertedLength)
        : revealedStarts(
            scanMarkdown(next).links,
            selection.start,
            selection.end,
          ),
    );
    pendingSelection.current = true;
    return true;
  };
  const accept = (entry: SkillEntry) => {
    if (
      !canEdit() ||
      !enabled ||
      !token ||
      !entry.selection ||
      composing.current ||
      contextChanged ||
      !inventoryKnown ||
      !inventory.some((current) => current.selection === entry.selection)
    )
      return;
    const {
      next,
      replacement,
      end,
      caret: nextCaret,
    } = skillCompletionEdit(value, token, entry.name);
    const nextBindings = [
      ...editedBindings(value, next, bindings).filter(
        (binding) => binding.start !== token.start,
      ),
      {
        start: token.start,
        end,
        token: replacement,
        selection: entry.selection!,
        stale: false,
        context: scope,
      },
    ];
    if (
      !commit(
        next,
        nextBindings,
        { start: nextCaret, end: nextCaret, direction: "none" },
        PromptEditKind.Discrete,
        false,
      )
    )
      return;
    setDismissed(true);
    queueMicrotask(() => {
      if (!canEdit()) return;
      const current = layoutRef.current;
      textarea.current?.focus({ preventScroll: true });
      textarea.current?.setSelectionRange(
        current.toDisplay(nextCaret),
        current.toDisplay(nextCaret),
      );
    });
  };
  const onChange = (nextDisplay: string, position: number) => {
    if (!canEdit()) return;
    const edit = diffDisplay(layout.display, nextDisplay, position);
    // While an IME owns the textarea, the composed edit is applied to the
    // frozen collapse model, which keeps the display equal to what the IME
    // produced whatever it typed, so the textarea is never rewritten.
    const held =
      composing.current && compositionCollapse.current?.canonical === value
        ? compositionCollapse.current
        : undefined;
    const frozen = held && editFrozenCollapse(held, edit);
    let range: TextRange, next: string, caretAt: number;
    let insertedLength = edit.inserted.length;
    if (frozen) {
      next = frozen.canonical;
      ({ range, insertedLength } = replacedRange(value, next));
      caretAt = buildLayout(
        next,
        [],
        Number.POSITIVE_INFINITY,
        frozen,
      ).toCanonical(position);
      console.debug("prompt composition edit", {
        collapsedLinks: frozen.links.length,
      });
    } else {
      range = canonicalRangeForDisplay(layout, edit.start, edit.end);
      next =
        value.slice(0, range.start) + edit.inserted + value.slice(range.end);
      // The reported caret decides where inside the inserted text it lands.
      caretAt =
        range.start +
        (position >= edit.start && position <= edit.start + edit.inserted.length
          ? position - edit.start
          : edit.inserted.length);
    }
    let converted = false;
    const typedDelimiter =
      !composing.current &&
      edit.start === edit.end &&
      edit.inserted.length === 1 &&
      /\s/u.test(edit.inserted);
    if (typedDelimiter) {
      const conversion = convertTypedUrl(next, range.start);
      // A conversion past the limit is dropped; the typed delimiter stays.
      if (conversion && promptBytes(conversion.text) <= MAX_PROMPT_BYTES) {
        next = conversion.text;
        caretAt = conversion.map(caretAt);
        converted = true;
        console.debug("prompt link conversion", { trigger: "delimiter" });
      }
    }
    const before = selectionRef.current,
      contiguous =
        before.start === before.end &&
        (before.start === range.end || before.start === range.start);
    const kind = converted
      ? PromptEditKind.Discrete
      : composing.current
        ? PromptEditKind.Composition
        : edit.start === edit.end &&
            edit.inserted.length === 1 &&
            !typedDelimiter &&
            contiguous
          ? PromptEditKind.Typing
          : edit.inserted === "" && range.end - range.start === 1 && contiguous
            ? PromptEditKind.Deleting
            : PromptEditKind.Discrete;
    const nextBindings = editedBindings(value, next, bindings);
    if (
      !commit(
        next,
        nextBindings,
        { start: caretAt, end: caretAt, direction: "none" },
        kind,
        kind !== PromptEditKind.Discrete,
        range,
        insertedLength,
        frozen,
      )
    )
      return;
    setDismissed(false);
    setSelected(0);
  };
  const syncSelection = () => {
    const input = textarea.current;
    if (!input) return;
    const current = layoutRef.current,
      start = current.toCanonical(input.selectionStart),
      end = current.toCanonical(input.selectionEnd);
    selectionRef.current = { start, end, direction: selectionDirection(input) };
    setCaret(start);
    if (composing.current) return;
    const wanted = revealedStarts(current.links, start, end);
    if (!sameStarts(wanted, revealedRef.current)) {
      pendingSelection.current = true;
      setRevealed(wanted);
    }
  };
  const runHistory = (action: HistoryAction) => {
    if (!canEdit() || composing.current) return;
    const current = snapshot();
    const target =
      action === HistoryAction.Undo
        ? history.current.undo(current)
        : history.current.redo(current);
    if (!target) return;
    if (change(target.value, target.bindings) === false) {
      history.current.revert(action);
      return;
    }
    const restored = target.bindings.map((binding) =>
      binding.context && binding.context !== scope
        ? { ...binding, stale: true }
        : binding,
    );
    setBindings(restored);
    selectionRef.current = target.selection;
    setCaret(target.selection.start);
    setRevealed(
      revealedStarts(
        scanMarkdown(target.value).links,
        target.selection.start,
        target.selection.end,
      ),
    );
    pendingSelection.current = true;
    setDismissed(true);
  };
  const historyRunner = useRef(runHistory);
  historyRunner.current = runHistory;
  useEffect(() => {
    const input = textarea.current;
    if (!input) return;
    // Programmatic link conversion clears the native undo stack, so native
    // history commands are routed to the adapter-owned history instead.
    const listener = (event: Event) => {
      const type = (event as InputEvent).inputType;
      if (type !== "historyUndo" && type !== "historyRedo") return;
      event.preventDefault();
      historyRunner.current(
        type === "historyUndo" ? HistoryAction.Undo : HistoryAction.Redo,
      );
    };
    input.addEventListener("beforeinput", listener);
    return () => input.removeEventListener("beforeinput", listener);
  }, [textarea, disabled, active]);
  const selectedCanonical = (): TextRange | undefined => {
    const input = textarea.current;
    if (!input || input.selectionStart === input.selectionEnd) return;
    return canonicalRangeForDisplay(
      layoutRef.current,
      input.selectionStart,
      input.selectionEnd,
    );
  };
  const onCopy = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const range = selectedCanonical();
    if (!range || composing.current) return;
    event.clipboardData.setData(
      "text/plain",
      value.slice(range.start, range.end),
    );
    event.preventDefault();
  };
  const onCut = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const range = selectedCanonical();
    if (!range || composing.current || !canEdit()) return;
    event.clipboardData.setData(
      "text/plain",
      value.slice(range.start, range.end),
    );
    event.preventDefault();
    const next = value.slice(0, range.start) + value.slice(range.end);
    commit(
      next,
      editedBindings(value, next, bindings),
      { start: range.start, end: range.start, direction: "none" },
      PromptEditKind.Discrete,
      false,
      range,
      0,
    );
  };
  const onPaste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const input = textarea.current;
    // Image paste ownership stays with the attachment input.
    if (
      !input ||
      !canEdit() ||
      composing.current ||
      event.clipboardData.files.length
    )
      return;
    const pasted = event.clipboardData
      .getData("text/plain")
      .replace(/\r\n?/gu, "\n");
    if (!/https?:\/\//iu.test(pasted)) return;
    const range = canonicalRangeForDisplay(
      layout,
      input.selectionStart,
      input.selectionEnd,
    );
    const inserted =
      value.slice(0, range.start) + pasted + value.slice(range.end);
    const conversion = convertBareUrls(
      inserted,
      range.start,
      range.start + pasted.length,
    );
    // Over the limit or nothing to convert: the native paste proceeds untouched.
    if (!conversion || promptBytes(conversion.text) > MAX_PROMPT_BYTES) return;
    event.preventDefault();
    const caretAt = conversion.map(range.start + pasted.length);
    console.debug("prompt link conversion", {
      trigger: "paste",
      count: conversion.count,
    });
    if (
      commit(
        conversion.text,
        editedBindings(value, conversion.text, bindings),
        { start: caretAt, end: caretAt, direction: "none" },
        PromptEditKind.Discrete,
        false,
        range,
        pasted.length,
      )
    ) {
      setDismissed(false);
      setSelected(0);
    }
  };
  const openLink = async (url: string) => {
    // One activation is one launch: no retry and no second browser.
    if (linkOpening.current) return;
    linkOpening.current = true;
    setLinkFailure(undefined);
    try {
      await openExternalUrl(url);
    } catch (error) {
      console.warn("prompt link open failed", externalLinkFailure(error));
      if (mounted.current) setLinkFailure(externalLinkFailure(error));
    } finally {
      linkOpening.current = false;
    }
  };
  const onClick = (event: MouseEvent<HTMLTextAreaElement>) => {
    const input = textarea.current;
    if (!input || composing.current || event.button !== 0) return;
    const mac = shortcutPlatform() === ShortcutPlatform.Mac;
    const exact =
      !event.altKey &&
      !event.shiftKey &&
      (mac ? event.metaKey && !event.ctrlKey : event.ctrlKey && !event.metaKey);
    if (!exact || input.selectionStart !== input.selectionEnd) return;
    // Without a native opener the link stays ordinary editable text.
    if (!externalLinksAvailable()) return;
    const link = linkAtCaret(
      layoutRef.current.links,
      layoutRef.current.toCanonical(input.selectionStart),
    );
    if (!link) return;
    event.preventDefault();
    void openLink(link.url);
  };
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (
      !canEdit() ||
      event.nativeEvent.isComposing ||
      composing.current ||
      event.nativeEvent.keyCode === 229 ||
      event.repeat ||
      event.getModifierState("AltGraph")
    )
      return false;
    const action = historyAction(event);
    if (action) {
      event.preventDefault();
      event.stopPropagation();
      runHistory(action);
      return true;
    }
    if (!token) return false;
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      setDismissed(true);
      return true;
    }
    if (
      candidates.length &&
      ["ArrowDown", "ArrowUp", "Enter", "Tab"].includes(event.key) &&
      // Plain Tab is intercepted only when an enabled row can be accepted;
      // otherwise it keeps native focus navigation.
      !(event.key === "Tab" && validIndex < 0) &&
      !event.shiftKey &&
      !event.metaKey &&
      !event.ctrlKey
    ) {
      event.preventDefault();
      event.stopPropagation();
      if (event.key === "ArrowDown") {
        keyboardNavigation.current = true;
        setSelected(
          enabledIndices[
            (enabledIndices.indexOf(validIndex) + 1) % enabledIndices.length
          ] ?? -1,
        );
      } else if (event.key === "ArrowUp") {
        keyboardNavigation.current = true;
        setSelected(
          enabledIndices[
            (enabledIndices.indexOf(validIndex) + enabledIndices.length - 1) %
              enabledIndices.length
          ] ?? -1,
        );
      } else if (validIndex >= 0) accept(candidates[validIndex]!);
      return true;
    }
    return false;
  };
  const distinct = new Map(
    bindings
      .filter(
        (binding) =>
          !binding.stale &&
          value.slice(binding.start, binding.end) === binding.token,
      )
      .map((binding) => [binding.selection.skillId, binding.selection]),
  );
  const blocked =
    (contextChanged && bindings.length > 0) ||
    bindings.some(
      (binding) =>
        binding.stale ||
        (binding.context &&
          (!machineId || !agentId || binding.context !== scope)) ||
        (!binding.ambiguous &&
          value.slice(binding.start, binding.end) !== binding.token),
    ) ||
    distinct.size > 16;
  useLayoutEffect(() => {
    if (!keyboardNavigation.current) return;
    keyboardNavigation.current = false;
    const container = completion.current;
    const option = container?.querySelector<HTMLElement>(
      `[aria-selected="true"]`,
    );
    if (!container || !option || !canEdit()) return;
    // Scroll only this list. scrollIntoView can move ancestor composers or the
    // page and displace the textarea while its original focus is retained.
    const bounds = container.getBoundingClientRect(),
      row = option.getBoundingClientRect();
    const scale = bounds.height / container.offsetHeight || 1;
    const top = bounds.top + container.clientTop * scale,
      bottom = top + container.clientHeight * scale;
    if (row.top < top) container.scrollTop += (row.top - top) / scale;
    else if (row.bottom > bottom)
      container.scrollTop += (row.bottom - bottom) / scale;
  }, [validIndex, visible, query.isFetching]);
  const list = visible ? (
    <div
      ref={completion}
      className="skill-completion"
      popover="manual"
      onKeyDown={(event) => {
        if (event.key !== "Escape" || event.nativeEvent.isComposing) return;
        event.preventDefault();
        event.stopPropagation();
        dismissCompletion();
        if (canEdit()) textarea.current?.focus({ preventScroll: true });
      }}
    >
      {enabled ? (
        query.isFetching ? (
          <p role="status">{copy("skills.loading")}</p>
        ) : !inventoryKnown ? (
          <>
            <p role="status">{copy("skills.unavailable")}</p>
            <button
              type="button"
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => {
                if (
                  canEdit() &&
                  enabled &&
                  machineId &&
                  agentId &&
                  !contextChanged
                )
                  void query.refetch();
              }}
            >
              {copy("skills.retry")}
            </button>
          </>
        ) : candidates.length ? (
          <ul role="listbox" id={id} aria-label={copy("skills.available")}>
            {candidates.map((entry, index) => (
              <li
                key={`${entry.selection!.inventoryId}:${entry.selection!.skillId}`}
                id={`${id}-${index}`}
                role="option"
                aria-selected={index === validIndex}
                aria-disabled={
                  entry.availability === SkillAvailability.Unavailable ||
                  undefined
                }
                data-availability={entry.availability}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => accept(entry)}
              >
                <strong className="skill-completion-name">{entry.name}</strong>
                <span className="skill-completion-description">
                  {entry.description}
                </span>
                {entry.availability === SkillAvailability.Unavailable ? (
                  <span className="skill-completion-status">
                    {copy("skills.entryUnavailable")}
                  </span>
                ) : null}
                <span className="skill-completion-provenance">
                  {entry.provenance === SkillProvenance.PROJECT
                    ? copy("skills.project")
                    : copy("skills.user")}
                </span>
              </li>
            ))}
          </ul>
        ) : (
          <p role="status">{copy("skills.empty")}</p>
        )
      ) : (
        <p role="status">{copy("skills.unsupported")}</p>
      )}
    </div>
  ) : null;
  return {
    selections: [...distinct.values()],
    blocked,
    list,
    clear: () => {
      if (canEdit()) setBindings([]);
    }, // Original receipt acceptance settles ownership before the pending render unlocks.
    replaceUnbound: (next: string, position: number, end = position) => {
      if (!canEdit()) return;
      if (
        !commit(
          next,
          [],
          { start: position, end, direction: "none" },
          PromptEditKind.Discrete,
          false,
        )
      )
        return;
      bindingsChanged?.([]);
      setDismissed(true);
      setSelected(0);
    },
    clearAccepted: () => {
      bindingsChanged?.([]);
      setBindings([]);
    },
    onChange,
    onKeyDown,
    // The textarea shows this collapsed display text; the draft stays canonical.
    displayValue: layout.display,
    draftSnapshot: (field: HTMLTextAreaElement) => ({
      text: value,
      start: layout.toCanonical(field.selectionStart),
      end: layout.toCanonical(field.selectionEnd),
    }),
    onSelect: () => {
      if (!canEdit()) return;
      syncSelection();
    },
    onCompositionStart: () => {
      if (!canEdit()) return;
      composing.current = true;
      compositionCollapse.current = freezeCollapse(layoutRef.current);
      setComposition(true);
      setDismissed(true);
    },
    onCompositionEnd: () => {
      // The textarea still shows the frozen display, so the selection maps
      // through the layout that produced it, not through a re-parsed one.
      const held = layoutRef.current;
      composing.current = false;
      compositionCollapse.current = undefined;
      setComposition(false);
      if (!canEdit()) return;
      const input = textarea.current;
      if (!input) return;
      const start = held.toCanonical(input.selectionStart),
        end = held.toCanonical(input.selectionEnd);
      selectionRef.current = {
        start,
        end,
        direction: selectionDirection(input),
      };
      setCaret(start);
      // The freeze is lifted: the final draft is parsed once, and the revealed
      // set is a function of that parse and the canonical selection alone. A
      // link that was revealed (or is new) is judged like every other link, so
      // one that still holds the caret stays revealed instead of collapsing
      // under it, and nothing from before the composition is carried over.
      const settled = scanDraft(held.canonical);
      settledScan.current = settled;
      const wanted = revealedStarts(settled.scan.links, start, end);
      if (!sameStarts(wanted, revealedRef.current)) setRevealed(wanted);
      console.debug("prompt composition end", {
        links: settled.scan.links.length,
        revealed: wanted.length,
      });
      // The display is recomputed from the final draft now that the freeze is
      // lifted, so the canonical selection is re-applied after that render.
      pendingSelection.current = true;
      setDismissed(false);
    },
    attributes: {
      "aria-describedby": unavailableCount ? `${id}-unavailable` : undefined,
      "aria-controls": visible && candidates.length ? id : undefined,
      "aria-autocomplete": "list" as const,
      onPaste,
      onCopy,
      onCut,
      onClick,
      "aria-expanded": visible,
      "aria-activedescendant":
        visible && validIndex >= 0 ? `${id}-${validIndex}` : undefined,
    },
    unavailable,
    unavailableCount,
    availability: inventoryKnown
      ? SkillAvailability.Available
      : SkillAvailability.Unknown,
    wrap: (child: ReactNode) => (
      <SkillText
        textarea={textarea}
        value={layout.display}
        links={layout.collapsed.map((link) => ({
          start: link.displayStart,
          end: link.displayEnd,
        }))}
        ranges={displayRanges(layout, unavailable)}
        unavailableCount={unavailableCount}
        selected={displayRanges(layout, selectedRanges)}
        descriptionId={`${id}-unavailable`}
        announcedCount={announcedCount}
      >
        {child}
      </SkillText>
    ),
    warning: (
      <>
        {blocked ? <p role="status">{copy("skills.reselect")}</p> : null}
        {linkFailure ? (
          <p className="external-link-error" role="alert">
            <span>{copy(externalLinkFailureMessages[linkFailure])}</span>
            <button
              type="button"
              aria-label={copy("external-link.dismiss")}
              onClick={() => setLinkFailure(undefined)}
            >
              <svg aria-hidden="true" focusable="false" viewBox="0 0 24 24">
                <path d="M6 6l12 12M18 6 6 18" />
              </svg>
            </button>
          </p>
        ) : null}
      </>
    ),
  };
}

// Skill ranges inside or touching a collapsed link are not painted; the link
// label owns that text. Other ranges move into display coordinates.
function displayRanges(
  layout: PromptLayout,
  ranges: SkillToken[],
): SkillToken[] {
  return ranges.flatMap((range) =>
    layout.collapsed.some(
      (link) => range.start < link.end && range.end > link.start,
    )
      ? []
      : [
          {
            ...range,
            start: layout.toDisplay(range.start),
            end: layout.toDisplay(range.end),
          },
        ],
  );
}

// The textarea retains editing and selection ownership; this sibling only paints text.
function SkillText({
  textarea,
  value,
  links,
  ranges,
  unavailableCount,
  selected,
  descriptionId,
  announcedCount,
  children,
}: {
  textarea: RefObject<HTMLTextAreaElement | null>;
  value: string;
  links: TextRange[];
  ranges: SkillToken[];
  unavailableCount: number;
  selected: SkillToken[];
  descriptionId: string;
  announcedCount?: number;
  children: ReactNode;
}) {
  const frame = useRef<HTMLDivElement>(null),
    content = useRef<HTMLDivElement>(null);
  // Reuse the one mirror. The analysis keeps absent and selected ranges disjoint
  // (confirmed absence wins) and bounded to 256 painted ranges (512 spans).
  const painted = [
    ...links.map((range) => ({ ...range, kind: "link" as const })),
    ...ranges.map((range) => ({ ...range, kind: "unavailable" as const })),
    ...selected.map((range) => ({ ...range, kind: "selected" as const })),
  ].sort((a, b) => a.start - b.start);
  // The appearance provider writes the composer typography data attributes in
  // its own layout effect, which runs after this child's layout effects.
  const { composer_size, composer_layout, density } =
    useAppearancePreferences();
  const realign = useRef<(() => void) | null>(null);
  useLayoutEffect(() => {
    const input = textarea.current,
      viewport = frame.current,
      text = content.current;
    if (!input || !viewport || !text) return;
    const align = () => {
      const style = getComputedStyle(input);
      Object.assign(viewport.style, {
        left: `${input.offsetLeft + input.clientLeft}px`,
        top: `${input.offsetTop + input.clientTop}px`,
        width: `${input.clientWidth}px`,
        height: `${input.clientHeight}px`,
      });
      for (const property of [
        "fontFamily",
        "fontSize",
        "fontWeight",
        "fontStyle",
        "fontVariant",
        "fontStretch",
        "fontKerning",
        "lineHeight",
        "letterSpacing",
        "wordSpacing",
        "textTransform",
        "direction",
        "paddingTop",
        "paddingRight",
        "paddingBottom",
        "paddingLeft",
        "textAlign",
        "textIndent",
        "tabSize",
        "wordBreak",
        "overflowWrap",
      ] as const)
        text.style[property] = style[property];
      text.style.width = `${input.clientWidth}px`;
      text.style.transform = `translate(${-input.scrollLeft}px, ${-input.scrollTop}px)`;
    };
    align();
    realign.current = align;
    const observer =
      typeof ResizeObserver === "undefined"
        ? undefined
        : new ResizeObserver(align);
    observer?.observe(input);
    input.addEventListener("scroll", align);
    window.addEventListener("resize", align);
    return () => {
      if (realign.current === align) realign.current = null;
      observer?.disconnect();
      input.removeEventListener("scroll", align);
      window.removeEventListener("resize", align);
    };
  }, [textarea, value, painted.length]);
  // Passive effects run after every layout effect of the commit, including the
  // provider's, so the recomputed style already reflects the new preference.
  useEffect(() => {
    realign.current?.();
  }, [composer_size, composer_layout, density]);
  let end = 0;
  const segments = painted.map((range) => {
    const preceding = value.slice(end, range.start);
    end = range.end;
    return (
      <span key={range.start}>
        {preceding}
        <span
          className={
            range.kind === "link"
              ? "prompt-markdown-link"
              : range.kind === "selected"
                ? "skill-token-selected"
                : "skill-token-unavailable"
          }
          data-skill-start={range.kind === "link" ? undefined : range.start}
          data-link-start={range.kind === "link" ? range.start : undefined}
        >
          {value.slice(range.start, range.end)}
        </span>
      </span>
    );
  });
  return (
    <div
      className="skill-textarea"
      data-skill-overlay={painted.length > 0 || undefined}
    >
      {children}
      <span
        className="sidebar-sr-only"
        role="status"
        aria-live="polite"
        aria-atomic="true"
      >
        {announcedCount === undefined
          ? null
          : announcedCount > 0
            ? copy("skills.tokensUnavailable", { count: announcedCount })
            : copy("skills.tokensAvailable")}
      </span>
      {unavailableCount ? (
        <span id={descriptionId} className="sidebar-sr-only" aria-hidden="true">
          {copy("skills.tokensUnavailable", { count: unavailableCount })}
        </span>
      ) : null}
      {painted.length ? (
        <div ref={frame} className="skill-text-overlay" aria-hidden="true">
          <div ref={content}>
            {segments}
            {value.slice(end)}
            {"\u200b"}
          </div>
        </div>
      ) : null}
    </div>
  );
}
