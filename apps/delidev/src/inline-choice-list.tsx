// SPDX-License-Identifier: Apache-2.0
import {
  useCallback,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import {
  ScrollContinuation,
  type ScrollContinuationQuery,
} from "./scroll-continuation";
import "./inline-choice-list.css";

/** Rows visible before the list scrolls internally. */
export const inlineChoiceVisibleRows = 5;

/** Decorative outline glyphs; the textual label is always the accessible name. */
export enum InlineChoiceIcon {
  Chat = "chat",
  Project = "project",
}

export interface InlineChoiceOption {
  id: string;
  label: string;
  icon: InlineChoiceIcon;
  disabled?: boolean;
}

export interface InlineChoiceAction {
  label: string;
  invoke: () => void;
}

const common = {
  "aria-hidden": true as const,
  focusable: false as const,
  viewBox: "0 0 24 24",
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 1.8,
  strokeLinecap: "round" as const,
  strokeLinejoin: "round" as const,
};

function ChoiceIcon({ icon }: { icon: InlineChoiceIcon }) {
  return icon === InlineChoiceIcon.Chat ? (
    <svg {...common}>
      <path d="M4 5h16v12H9l-5 4z" />
      <path d="M8 9h8M8 13h5" />
    </svg>
  ) : (
    <svg {...common}>
      <rect x="3" y="4" width="18" height="13" rx="1.5" />
      <path d="M8 21h8M12 17v4" />
    </svg>
  );
}

/** An always-expanded single-select list. Choices are display projections: the
 * owner still validates every accepted identity, and selecting the identity that
 * is already accepted never reaches `change`. */
export function InlineChoiceList({
  label,
  options,
  value,
  change,
  query,
  active,
  disabled = false,
  action,
}: {
  label: string;
  options: InlineChoiceOption[];
  value: string;
  change: (id: string) => void;
  query: ScrollContinuationQuery;
  active: boolean;
  disabled?: boolean;
  /** Optional icon button at the right of the label. */
  action?: InlineChoiceAction;
}) {
  const id = useId(),
    viewport = useRef<HTMLDivElement>(null),
    list = useRef<HTMLDivElement>(null);
  const [focusId, setFocusId] = useState("");
  const [limit, setLimit] = useState<number>();
  const available = active && !disabled;
  const enabled = options.filter((option) => !option.disabled);
  // Roving tab stop: the last focused row, else the accepted row, else the first.
  const stop = (
    enabled.find((option) => option.id === focusId) ??
    enabled.find((option) => option.id === value) ??
    enabled[0]
  )?.id;
  const rowCount = options.length;
  // A short list never scrolls: it keeps its natural height even when a wrapped
  // row makes five rows taller than the stylesheet's five-row fallback cap.
  const short = rowCount <= inlineChoiceVisibleRows;
  // Size the viewport to the first five rendered rows so wrapped names grow
  // their own rows instead of being clipped. Layout-less environments report 0
  // and keep the stylesheet fallback.
  useLayoutEffect(() => {
    const rows = list.current;
    if (!rows) return;
    const measure = () => {
      if (rows.children.length <= inlineChoiceVisibleRows) {
        setLimit(undefined);
        return;
      }
      const first = rows.children[0] as HTMLElement,
        last = rows.children[inlineChoiceVisibleRows - 1] as HTMLElement;
      const height = last.offsetTop + last.offsetHeight - first.offsetTop;
      setLimit(height > 0 ? height : undefined);
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(rows);
    return () => observer.disconnect();
  }, [rowCount]);
  const rows = useCallback(
    () => [
      ...(list.current?.querySelectorAll<HTMLElement>(
        "[role='option']:not([data-choice-disabled])",
      ) ?? []),
    ],
    [],
  );
  const choose = (option: InlineChoiceOption) => {
    if (!available || option.disabled) return;
    setFocusId(option.id);
    if (option.id !== value) change(option.id);
  };
  const keyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    // A locked or inactive list ignores every key: focus and the roving tab stop
    // stay where they were so they are still correct once the lock clears.
    if (!available) return;
    if (
      event.nativeEvent.isComposing ||
      event.nativeEvent.keyCode === 229 ||
      event.altKey ||
      event.ctrlKey ||
      event.metaKey
    )
      return;
    const target = (event.target as HTMLElement).closest<HTMLElement>(
      "[role='option']",
    );
    if (!target || !list.current?.contains(target)) return;
    if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
      event.preventDefault();
      const nodes = rows(),
        index = nodes.indexOf(target);
      const next =
        event.key === "Home"
          ? 0
          : event.key === "End"
            ? nodes.length - 1
            : event.key === "ArrowDown"
              ? Math.min(index + 1, nodes.length - 1)
              : Math.max(index - 1, 0);
      nodes[next]?.focus();
      return;
    }
    if ((event.key === "Enter" || event.key === " ") && !event.repeat) {
      event.preventDefault();
      const option = options.find(
        (candidate) => candidate.id === target.dataset.pickerId,
      );
      if (option) choose(option);
    }
  };
  const heading = (
    <div className="inline-choice-heading">
      <span id={`${id}-label`}>{label}</span>
      {action ? (
        <button
          type="button"
          className="inline-choice-action"
          aria-label={action.label}
          title={action.label}
          disabled={!active}
          onClick={(event) => {
            // WebKit does not focus a button on click; the modal host restores
            // focus to the element that was active when it opened.
            event.currentTarget.focus({ preventScroll: true });
            action.invoke();
          }}
        >
          <svg
            aria-hidden="true"
            focusable="false"
            viewBox="0 0 16 16"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
          >
            <path d="M8 3v10M3 8h10" />
          </svg>
        </button>
      ) : null}
    </div>
  );
  return (
    <div className="inline-choice">
      {heading}
      <div className="inline-choice-panel">
        <div
          ref={viewport}
          className="inline-choice-viewport"
          data-short={short || undefined}
          style={
            short || limit === undefined ? undefined : { maxHeight: limit }
          }
        >
          <div
            ref={list}
            role="listbox"
            id={`${id}-choices`}
            aria-labelledby={`${id}-label`}
            aria-disabled={!available || undefined}
            data-value={value}
            onKeyDown={keyDown}
          >
            {options.map((option, index) => (
              <div
                key={option.id}
                id={`${id}-option-${index}`}
                role="option"
                aria-selected={value === option.id}
                aria-disabled={!available || option.disabled || undefined}
                data-choice-disabled={option.disabled || undefined}
                data-picker-id={option.id}
                tabIndex={option.id === stop ? 0 : -1}
                onFocus={() => setFocusId(option.id)}
                onClick={() => choose(option)}
              >
                <span className="inline-choice-icon">
                  <ChoiceIcon icon={option.icon} />
                </span>
                <span className="inline-choice-label">{option.label}</span>
                <span className="inline-choice-check">
                  {value === option.id ? (
                    <svg
                      aria-hidden="true"
                      focusable="false"
                      viewBox="0 0 16 16"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="1.8"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                    >
                      <path d="m3 8.5 3.2 3.2L13 4.8" />
                    </svg>
                  ) : null}
                </span>
              </div>
            ))}
          </div>
          <ScrollContinuation
            query={query}
            label={label}
            root={viewport}
            active={available}
          />
        </div>
      </div>
    </div>
  );
}
