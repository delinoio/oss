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
import { copy, useLocale } from "./localization";
import { ExternalLinkAlert } from "./external-link-text";
import {
  externalLinkFailure,
  externalLinksAvailable,
  openExternalUrl,
  type ExternalLinkFailure,
} from "./external-links";
import {
  EditHistory,
  EditKind,
  type HistorySelection,
  type HistorySnapshot,
} from "./prompt-edit-history";
import {
  MarkdownProjection,
  canonicalEditFromDisplay,
  convertBareUrls,
  convertDelimitedWords,
  isUrlDelimiter,
  joinKeepingLineEndings,
  MAX_COLLAPSED_LINKS,
  projectMarkdown,
  scanMarkdown,
  selectionRevealsLink,
  type MarkdownLink,
} from "./prompt-markdown-links";
import {
  skillCompletionAnchorUsable,
  useSkillCompletionOverlay,
} from "./skill-completion-overlay";

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
const EMPTY_SELECTION: HistorySelection = {
  start: 0,
  end: 0,
  direction: "none",
};
const collapsedAt = (offset: number): HistorySelection => ({
  start: offset,
  end: offset,
  direction: "none",
});
/** Command key on Apple platforms, Control elsewhere. */
export function isApplePlatform(): boolean {
  return /Mac|iPhone|iPad/u.test(
    typeof navigator === "undefined"
      ? ""
      : navigator.platform || navigator.userAgent,
  );
}
interface ModifierState {
  metaKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
}
/** The platform command modifier alone, with no other modifier held. */
function platformModifierOnly(event: ModifierState): boolean {
  return isApplePlatform()
    ? event.metaKey && !event.ctrlKey && !event.altKey && !event.shiftKey
    : event.ctrlKey && !event.metaKey && !event.altKey && !event.shiftKey;
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
    [selection, setSelectionState] =
      useState<HistorySelection>(EMPTY_SELECTION),
    [dismissed, setDismissed] = useState(false),
    [selected, setSelected] = useState(0);
  // Canonical selection; the textarea reports display offsets that the
  // projection translates before they reach this state.
  const selectionRef = useRef(selection);
  const updateSelection = (next: HistorySelection) => {
    const prior = selectionRef.current;
    if (
      prior.start === next.start &&
      prior.end === next.end &&
      prior.direction === next.direction
    )
      return;
    selectionRef.current = next;
    setSelectionState(next);
  };
  const caret = Math.min(selection.start, value.length);
  useEffect(() => {
    bindingsChanged?.(bindings);
  }, [bindings, bindingsChanged]);
  const composing = useRef(false),
    editable = useRef(false);
  const [composition, setComposition] = useState(false);
  const completion = useRef<HTMLDivElement>(null),
    keyboardNavigation = useRef(false);
  const dismissCompletion = useCallback(() => setDismissed(true), []);
  editable.current = active && !disabled;
  const canEdit = () =>
    editable.current && skillCompletionAnchorUsable(textarea.current);
  // Markdown projection: `value` is canonical Markdown; the textarea shows the
  // projection in which unselected valid links collapse to their labels.
  const [history] = useState(() => new EditHistory<SkillTokenBinding>());
  const scan = useMemo(() => scanMarkdown(value), [value]);
  const revealStart = Math.min(selection.start, value.length),
    revealEnd = Math.min(selection.end, value.length);
  const linkProjection = useMemo(
    () =>
      projectMarkdown(
        value,
        scan.links,
        (link) =>
          !selectionRevealsLink(link, { start: revealStart, end: revealEnd }),
      ),
    [value, scan, revealStart, revealEnd],
  );
  // During IME composition the displayed text is frozen to what the native
  // field holds, so no collapse or reveal rewrites the composition.
  const compositionProjection = useRef<MarkdownProjection | undefined>(
    undefined,
  );
  const compositionSession = useRef(0),
    compositionAnchor = useRef(0),
    lateComposition = useRef(false);
  const projection = compositionProjection.current ?? linkProjection;
  const projectionRef = useRef(projection),
    valueRef = useRef(value);
  projectionRef.current = projection;
  valueRef.current = value;
  const [, rerender] = useState(0);
  const expected = useRef(value),
    pending = useRef<{ selection: HistorySelection; focus: boolean }>(
      undefined,
    ),
    appliedDisplay = useRef<string>(undefined);
  const [linkFailure, setLinkFailure] = useState<ExternalLinkFailure>();
  const openingLink = useRef(false),
    pendingOpen = useRef<string>(undefined),
    mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const freshProjection = (text: string, at: HistorySelection) =>
    projectMarkdown(
      text,
      scanMarkdown(text).links,
      (link) => !selectionRevealsLink(link, at),
    );
  // Reads the native selection back into canonical offsets. A field that does
  // not render the projection keeps the last known canonical selection.
  const readSelection = (): HistorySelection => {
    const field = textarea.current,
      map = projectionRef.current;
    if (!field || field.value !== map.text) return selectionRef.current;
    return {
      start: map.toSource(field.selectionStart),
      end: map.toSource(field.selectionEnd),
      direction:
        field.selectionDirection === "forward" ||
        field.selectionDirection === "backward"
          ? field.selectionDirection
          : "none",
    };
  };
  const applySelection = (next: HistorySelection) => {
    const field = textarea.current,
      map = projectionRef.current,
      length = valueRef.current.length;
    if (!field || field.value !== map.text) return;
    const start = map.toDisplay(Math.min(next.start, length)),
      end = map.toDisplay(Math.min(next.end, length));
    if (
      field.selectionStart !== start ||
      field.selectionEnd !== end ||
      (next.direction !== "none" && field.selectionDirection !== next.direction)
    )
      field.setSelectionRange(start, end, next.direction);
  };
  // Runs after every commit: detects owner replacements (submit, restoration,
  // recall), then restores the native selection whenever the displayed text
  // was rewritten or an edit requested a selection.
  useLayoutEffect(() => {
    if (expected.current !== value) {
      expected.current = value;
      history.reset();
      if (!pending.current) {
        const at = collapsedAt(value.length);
        pending.current = { selection: at, focus: false };
        updateSelection(at);
      }
    }
    const first = appliedDisplay.current === undefined,
      changed = appliedDisplay.current !== projection.text;
    appliedDisplay.current = projection.text;
    const request = pending.current;
    if (composing.current || (!request && (first || !changed))) return;
    pending.current = undefined;
    if (request?.focus && canEdit())
      textarea.current?.focus({ preventScroll: true });
    applySelection(request?.selection ?? selectionRef.current);
  });
  const snapshot = (): HistorySnapshot<SkillTokenBinding> => ({
    text: value,
    bindings,
    selection: selectionRef.current,
  });
  // The caret token describes where the user is typing and is independent of
  // panel dismissal; only the panel, query and keyboard ownership honor dismissal.
  const caretToken = !composing.current ? skillToken(value, caret) : undefined;
  const token = !dismissed ? caretToken : undefined;
  const contextChanged =
    previousScope.current.scope !== scope ||
    (!retainTransportContext && previousScope.current.transport !== transport);
  useEffect(() => {
    if (contextChanged && machineId && agentId) {
      previousScope.current = { scope, transport };
      setBindings((prior) =>
        prior.map((binding) => ({ ...binding, stale: true })),
      );
      setSelected(0);
    }
  }, [scope, transport, contextChanged, machineId, agentId]);
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
    const before = snapshot();
    if (change(next, nextBindings) === false) return;
    const at = collapsedAt(nextCaret);
    expected.current = next;
    history.record(
      EditKind.Completion,
      before,
      { text: next, bindings: nextBindings, selection: at },
      { seal: true },
    );
    setBindings(nextBindings);
    updateSelection(at);
    pending.current = { selection: at, focus: true };
    setDismissed(true);
    queueMicrotask(() => {
      if (!canEdit()) return;
      textarea.current?.focus({ preventScroll: true });
      applySelection(at);
    });
  };
  // One accepted canonical edit: admission by the owner, one history entry and
  // bindings moved through the edit. The first candidate the owner admits wins,
  // so a conversion that exceeds the prompt limit falls back to the plain edit.
  const transact = ({
    candidates,
    kindFor,
    selectionFor,
    seal,
    session,
    amend,
    apply = true,
  }: {
    candidates: string[];
    kindFor: (text: string) => EditKind;
    selectionFor: (text: string) => HistorySelection;
    seal: boolean;
    session?: number;
    amend?: number;
    apply?: boolean;
  }): boolean => {
    const before = snapshot();
    for (const text of candidates) {
      const nextBindings = editedBindings(value, text, bindings);
      if (change(text, nextBindings) === false) continue;
      const at = selectionFor(text),
        after = { text, bindings: nextBindings, selection: at };
      expected.current = text;
      if (text === value) {
        // Fallback to the already accepted text after the owner refused every
        // converted candidate: it re-offers the accepted draft so the owner
        // drops its refusal state, and it is never a new history entry.
        if (amend === undefined || !history.amend(amend, after)) history.seal();
      } else if (amend === undefined || !history.amend(amend, after))
        history.record(kindFor(text), before, after, { seal, session });
      setBindings(nextBindings);
      updateSelection(at);
      if (apply) pending.current = { selection: at, focus: false };
      setDismissed(false);
      setSelected(0);
      return true;
    }
    return false;
  };
  // `nextDisplay` is the native textarea text (the projection after the edit)
  // and `position` its caret; the edit is translated to canonical Markdown.
  const onChange = (nextDisplay: string, position: number) => {
    if (!canEdit()) return;
    const map = projectionRef.current,
      edit = canonicalEditFromDisplay(map, nextDisplay, position),
      plain = joinKeepingLineEndings(
        value.slice(0, edit.start),
        edit.inserted + value.slice(edit.end),
      ),
      caretAfter = edit.caret(position),
      composingNow = composing.current;
    if (plain === value) {
      // Nothing changed in the draft (for example a caret-only update).
      updateSelection(collapsedAt(caretAfter));
      return;
    }
    // A typed space or newline completes the URL before it. Text committed by
    // an IME is processed once composition has ended, never during it.
    let converted: string | undefined;
    if (!composingNow) {
      const typed = edit.inserted.length === 1 && isUrlDelimiter(edit.inserted);
      if (typed || (lateComposition.current && edit.inserted)) {
        const result = convertDelimitedWords(
          plain,
          edit.start,
          edit.start + edit.inserted.length,
        );
        if (result.changed) converted = result.text;
      }
    }
    const accepted = transact({
      candidates: converted ? [converted, plain] : [plain],
      kindFor: (text) =>
        text !== plain
          ? EditKind.Conversion
          : composingNow
            ? EditKind.Composition
            : edit.inserted
              ? EditKind.Typing
              : EditKind.Deletion,
      selectionFor: (text) =>
        collapsedAt(caretAfter + (text.length - plain.length)),
      // A word boundary seals the entry whether the whitespace was typed or
      // removed, so a run of Backspace/Delete splits at each removed space.
      seal:
        !composingNow &&
        (converted !== undefined ||
          /\s/u.test(edit.inserted) ||
          /\s/u.test(value.slice(edit.start, edit.end))),
      session: composingNow ? compositionSession.current : undefined,
      apply: !composingNow,
    });
    if (!accepted || !composingNow) return;
    // Keep the frozen projection aligned with the native text being composed.
    compositionProjection.current =
      map.shifted(
        edit.displayStart,
        edit.displayEnd,
        edit.inserted,
        edit.inserted.length - (edit.end - edit.start),
      ) ?? freshProjection(plain, collapsedAt(caretAfter));
    rerender((count) => count + 1);
  };
  // Replaces the selected canonical range with plain text (paste, cut).
  const replaceSelected = (
    text: string,
    range: HistorySelection,
    kind: EditKind,
  ): boolean => {
    const plain = joinKeepingLineEndings(
        value.slice(0, range.start),
        text + value.slice(range.end),
      ),
      end = range.start + text.length;
    const result =
      kind === EditKind.Paste
        ? convertBareUrls(plain, range.start, end)
        : { text: plain, changed: false };
    return transact({
      candidates: result.changed ? [result.text, plain] : [plain],
      kindFor: (admitted) => (admitted === plain ? kind : EditKind.Conversion),
      selectionFor: (admitted) =>
        collapsedAt(end + (admitted.length - plain.length)),
      seal: true,
    });
  };
  const stepHistory = (redo: boolean) => {
    if (!canEdit() || composing.current) return;
    const snap = redo ? history.peekRedo() : history.peekUndo();
    if (!snap) return;
    const restored = snap.bindings.map((binding) => ({
      ...binding,
      stale:
        binding.stale ||
        (binding.context !== undefined && binding.context !== scope),
    }));
    if (change(snap.text, restored) === false) return;
    if (redo) history.commitRedo();
    else history.commitUndo();
    expected.current = snap.text;
    setBindings(restored);
    updateSelection(snap.selection);
    pending.current = { selection: snap.selection, focus: false };
    setDismissed(true);
    setSelected(0);
  };
  const isUndoKey = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    const key = (event.key ?? "").toLowerCase(),
      apple = isApplePlatform();
    if (
      event.altKey ||
      (apple
        ? !event.metaKey || event.ctrlKey
        : !event.ctrlKey || event.metaKey)
    )
      return undefined;
    // Non-Latin layouts report another `key`; the physical code stays stable.
    if (key === "z" || event.code === "KeyZ")
      return event.shiftKey ? "redo" : "undo";
    if (!apple && (key === "y" || event.code === "KeyY") && !event.shiftKey)
      return "redo";
    return undefined;
  };
  const openLink = async (url: string) => {
    // One activation is one launch: no retry and no fallback.
    if (openingLink.current) return;
    openingLink.current = true;
    setLinkFailure(undefined);
    try {
      await openExternalUrl(url);
    } catch (error) {
      if (mounted.current) setLinkFailure(externalLinkFailure(error));
    } finally {
      openingLink.current = false;
    }
  };
  // Pointer hit-testing uses the painted link spans, which exist for both
  // collapsed labels and revealed source.
  const linkAtPoint = (x: number, y: number): MarkdownLink | undefined => {
    const spans =
      textarea.current
        ?.closest(".skill-textarea")
        ?.querySelectorAll<HTMLElement>("[data-markdown-link]") ?? [];
    for (const span of spans) {
      const hit = [...span.getClientRects()].some(
        (rect) =>
          x >= rect.left &&
          x <= rect.right &&
          y >= rect.top &&
          y <= rect.bottom,
      );
      if (hit)
        return scan.links.find(
          (link) => link.start === Number(span.dataset.markdownLink),
        );
    }
    return undefined;
  };
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    // Undo and redo are adapter-owned: native history cannot follow link
    // reveal and collapse because programmatic value writes discard it.
    if (
      canEdit() &&
      !composing.current &&
      !event.nativeEvent.isComposing &&
      event.nativeEvent.keyCode !== 229
    ) {
      const step = isUndoKey(event);
      if (step) {
        event.preventDefault();
        stepHistory(step === "redo");
        return true;
      }
    }
    if (
      !canEdit() ||
      event.nativeEvent.isComposing ||
      composing.current ||
      event.nativeEvent.keyCode === 229 ||
      event.repeat ||
      event.getModifierState("AltGraph")
    )
      return false;
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
  // Browser history commands (Edit menu, context menu) arrive as `beforeinput`
  // and run through the same adapter-owned history as the keyboard shortcuts.
  useLayoutEffect(() => {
    const field = textarea.current;
    if (!field) return;
    const listener = (event: Event) => {
      const type = (event as InputEvent).inputType;
      if (type !== "historyUndo" && type !== "historyRedo") return;
      event.preventDefault();
      stepHistory(type === "historyRedo");
    };
    field.addEventListener("beforeinput", listener);
    return () => field.removeEventListener("beforeinput", listener);
  });
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
      if (!canEdit()) return;
      history.reset();
      setBindings([]);
    }, // Original receipt acceptance settles ownership before the pending render unlocks.
    replaceUnbound: (next: string, position: number) => {
      if (!canEdit()) return;
      if (change(next, []) === false) return;
      expected.current = next;
      history.reset();
      setBindings([]);
      bindingsChanged?.([]);
      const at = collapsedAt(position);
      updateSelection(at);
      pending.current = { selection: at, focus: false };
      setDismissed(true);
      setSelected(0);
    },
    clearAccepted: () => {
      history.reset();
      bindingsChanged?.([]);
      setBindings([]);
    },
    /** Text shown by the native textarea: the canonical draft's projection. */
    value: projection.text,
    /** Canonical draft text with the canonical selection, for owners that read the field. */
    readCanonical: () => {
      const at = readSelection();
      return { text: value, start: at.start, end: at.end };
    },
    /** Selects a canonical range, translated to the displayed text. */
    select: (start: number, end: number) => {
      if (!canEdit()) return;
      const at: HistorySelection = { start, end, direction: "none" };
      updateSelection(at);
      pending.current = { selection: at, focus: false };
      applySelection(at);
      rerender((count) => count + 1);
    },
    onChange,
    onKeyDown,
    onSelect: () => {
      if (!canEdit()) return;
      updateSelection(readSelection());
    },
    onCompositionStart: () => {
      if (!canEdit()) return;
      composing.current = true;
      compositionProjection.current = projectionRef.current;
      compositionSession.current += 1;
      compositionAnchor.current = selectionRef.current.start;
      setComposition(true);
      setDismissed(true);
    },
    onCompositionEnd: () => {
      const session = compositionSession.current,
        frozen = compositionProjection.current;
      composing.current = false;
      setComposition(false);
      if (!canEdit()) {
        compositionProjection.current = undefined;
        return;
      }
      // Still the composed text: read the native caret through the frozen map.
      const at = readSelection();
      compositionProjection.current = undefined;
      if (frozen) {
        updateSelection(at);
        pending.current = { selection: at, focus: false };
        // Text a WebKit-style late `input` commits after this event is
        // processed with the same rules, in this task only.
        lateComposition.current = true;
        setTimeout(() => {
          lateComposition.current = false;
        }, 0);
        const result = convertDelimitedWords(
          value,
          compositionAnchor.current,
          at.start,
        );
        // The accepted composed text is the fallback when the owner refuses
        // the expanded Markdown (for example at the prompt limit).
        if (result.changed)
          transact({
            candidates: [result.text, value],
            kindFor: () => EditKind.Conversion,
            selectionFor: (text) =>
              collapsedAt(at.start + text.length - value.length),
            seal: true,
            amend: session,
          });
      }
      setDismissed(false);
      rerender((count) => count + 1);
    },
    /** Native handlers for paste, copy, cut and modifier-click activation. */
    input: {
      onPaste: (event: ClipboardEvent<HTMLTextAreaElement>) => {
        const data = event.clipboardData;
        // Clipboard files stay with the attachment owner, which sees the
        // event after this handler; mixed or text-less data is left native.
        if (!canEdit() || composing.current || !data || data.files?.length)
          return;
        const text = data.getData("text/plain").replace(/\r\n?/gu, "\n");
        if (!text) return;
        event.preventDefault();
        replaceSelected(text, readSelection(), EditKind.Paste);
      },
      onCopy: (event: ClipboardEvent<HTMLTextAreaElement>) => {
        const at = readSelection(),
          data = event.clipboardData;
        if (at.start === at.end || !data) return;
        data.setData("text/plain", value.slice(at.start, at.end));
        event.preventDefault();
      },
      onCut: (event: ClipboardEvent<HTMLTextAreaElement>) => {
        const at = readSelection(),
          data = event.clipboardData;
        if (!canEdit() || composing.current || at.start === at.end || !data)
          return;
        event.preventDefault();
        const text = value.slice(at.start, at.end);
        if (replaceSelected("", at, EditKind.Cut))
          data.setData("text/plain", text);
      },
      onMouseDown: (event: MouseEvent<HTMLTextAreaElement>) => {
        pendingOpen.current = undefined;
        if (
          event.button !== 0 ||
          !platformModifierOnly(event) ||
          !externalLinksAvailable()
        )
          return;
        const link = linkAtPoint(event.clientX, event.clientY);
        if (!link) return;
        // The modifier gesture only opens: no caret move, focus or reveal.
        event.preventDefault();
        pendingOpen.current = link.url;
      },
      onClick: (event: MouseEvent<HTMLTextAreaElement>) => {
        const url = pendingOpen.current;
        pendingOpen.current = undefined;
        if (!url || event.button !== 0 || !platformModifierOnly(event)) return;
        event.preventDefault();
        event.stopPropagation();
        void openLink(url);
      },
    },
    attributes: {
      "aria-describedby": unavailableCount ? `${id}-unavailable` : undefined,
      "aria-controls": visible && candidates.length ? id : undefined,
      "aria-autocomplete": "list" as const,
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
        value={projection.text}
        projection={projection}
        links={scan.links}
        ranges={unavailable}
        unavailableCount={unavailableCount}
        selected={selectedRanges}
        descriptionId={`${id}-unavailable`}
        announcedCount={announcedCount}
      >
        {child}
      </SkillText>
    ),
    warning:
      blocked || linkFailure ? (
        <>
          {blocked ? <p role="status">{copy("skills.reselect")}</p> : null}
          {linkFailure ? (
            <ExternalLinkAlert
              failure={linkFailure}
              dismiss={() => setLinkFailure(undefined)}
            />
          ) : null}
        </>
      ) : null,
  };
}

// Source links painted at most this many times keeps the mirror bounded.
const MAX_PAINTED_LINKS = MAX_COLLAPSED_LINKS;
interface PaintedItem {
  start: number;
  end: number;
  className: string;
  attributes: Record<string, string | number>;
}
// The textarea retains editing, selection and focus ownership; this sibling only paints text.
function SkillText({
  textarea,
  value,
  projection,
  links,
  ranges,
  unavailableCount,
  selected,
  descriptionId,
  announcedCount,
  children,
}: {
  textarea: RefObject<HTMLTextAreaElement | null>;
  // Displayed text: the same string the native textarea holds.
  value: string;
  projection: MarkdownProjection;
  links: readonly MarkdownLink[];
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
  // Skill ranges are canonical offsets; every painted span is positioned in the
  // displayed text. A skill token that touches link syntax is left unpainted so
  // spans never overlap.
  const skillItems: PaintedItem[] = [
    ...ranges.map((range) => ({ ...range, selected: false })),
    ...selected.map((range) => ({ ...range, selected: true })),
  ]
    .filter(
      (range) =>
        !links.some((link) => link.start < range.end && link.end > range.start),
    )
    .map((range) => ({
      start: projection.toDisplay(range.start),
      end: projection.toDisplay(range.end),
      className: range.selected
        ? "skill-token-selected"
        : "skill-token-unavailable",
      attributes: { "data-skill-start": range.start },
    }));
  const collapsed = new Map(
    projection.regions.map((region) => [region.srcStart, region]),
  );
  // Every collapsed region is painted; revealed source is painted for the
  // first MAX_PAINTED_LINKS links so hit-testing stays bounded.
  const linkItems: PaintedItem[] = [];
  let sourceCount = 0;
  for (const link of links) {
    const region = collapsed.get(link.start);
    if (region)
      linkItems.push({
        start: region.displayStart,
        end: region.displayStart + region.label.length,
        className: "markdown-link-label",
        attributes: {
          "data-markdown-link": link.start,
          "data-markdown-state": "collapsed",
        },
      });
    else if (sourceCount < MAX_PAINTED_LINKS) {
      sourceCount++;
      linkItems.push({
        start: projection.toDisplay(link.start),
        end: projection.toDisplay(link.end),
        className: "markdown-link-source",
        attributes: {
          "data-markdown-link": link.start,
          "data-markdown-state": "source",
        },
      });
    }
  }
  const painted = [...skillItems, ...linkItems].sort(
    (a, b) => a.start - b.start,
  );
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
  const segments = painted.map((item) => {
    const preceding = value.slice(end, item.start);
    end = item.end;
    return (
      <span key={item.start}>
        {preceding}
        <span className={item.className} {...item.attributes}>
          {value.slice(item.start, item.end)}
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
