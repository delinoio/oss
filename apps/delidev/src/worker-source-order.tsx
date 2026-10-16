// SPDX-License-Identifier: Apache-2.0
import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useTransport } from "@connectrpc/connect-query";
import { copy, useLocale } from "./localization";
import { useSettingsOpening } from "./settings-lifetime";

type Movement = {
  id: string;
  original: string[];
  preview: string[];
  pointer?: number;
  authority: object;
};
/** What the owner needs to place one ordered item and its reorder grip. */
export type WorkerSourceOrderItem = {
  id: string;
  /** Zero-based position in the live (preview) order. */
  index: number;
  moving: boolean;
  /** The reorder grip. It is a standalone button the owner must not nest in another button. */
  grip: ReactNode;
};
/**
 * Reorder controller for the Accounts step. It owns the ordered list element,
 * the movement state machine (pointer and keyboard preview/commit/cancel) and
 * the live region; the owner renders each item's card around the supplied grip.
 * Preview order belongs to this view; only an explicit commit changes the draft.
 */
export function WorkerSourceOrder({
  ids,
  names,
  active,
  change,
  editable = true,
  renderItem,
}: {
  ids: string[];
  names: ReadonlyMap<string, string>;
  active: boolean;
  change: (ids: string[]) => void;
  editable?: boolean;
  renderItem: (item: WorkerSourceOrderItem) => ReactNode;
}) {
  useLocale();
  const transport = useTransport(),
    opening = useSettingsOpening();
  const root = useRef<HTMLOListElement>(null),
    handles = useRef(new Map<string, HTMLButtonElement>());
  const current = useRef({ ids, active, editable, transport, change });
  current.current = { ids, active, editable, transport, change };
  const movement = useRef<Movement | undefined>(undefined);
  const [preview, setPreview] = useState<string[]>(),
    [status, announce] = useState("");
  const allowed = () => {
    const node = root.current;
    return (
      current.current.active &&
      current.current.editable &&
      !opening?.disposed &&
      !!node?.isConnected &&
      !node.closest(
        '[hidden], [inert], fieldset:disabled, [aria-disabled="true"]',
      )
    );
  };
  const focus = (id: string) =>
    handles.current.get(id)?.focus({ preventScroll: true });
  const finish = (commit: boolean) => {
    const value = movement.current;
    if (!value) return;
    const valid =
      allowed() &&
      value.authority === current.current.transport &&
      JSON.stringify(value.original) === JSON.stringify(current.current.ids);
    movement.current = undefined;
    setPreview(undefined);
    if (
      commit &&
      valid &&
      value.preview.some((id, i) => id !== value.original[i])
    )
      current.current.change(value.preview);
    announce(
      copy(
        commit && valid
          ? "agent-worker-wizard.sourceOrdercompleted"
          : "agent-worker-wizard.sourceOrdercanceled",
        {
          position:
            (commit && valid ? value.preview : value.original).indexOf(
              value.id,
            ) + 1,
        },
      ),
    );
    if (valid) focus(value.id);
  };
  const begin = (id: string, pointer?: number) => {
    if (!allowed() || current.current.ids.length < 2 || movement.current)
      return;
    const original = [...current.current.ids];
    movement.current = {
      id,
      original,
      preview: original,
      pointer,
      authority: current.current.transport,
    };
    setPreview(original);
    announce(
      copy("agent-worker-wizard.sourceOrderpicked", {
        position: original.indexOf(id) + 1,
      }),
    );
    focus(id);
  };
  const move = (index: number) => {
    const value = movement.current;
    if (!value) return;
    if (
      !allowed() ||
      value.authority !== current.current.transport ||
      JSON.stringify(value.original) !== JSON.stringify(current.current.ids)
    ) {
      finish(false);
      return;
    }
    const next = value.preview.filter((id) => id !== value.id);
    next.splice(Math.max(0, Math.min(next.length, index)), 0, value.id);
    value.preview = next;
    setPreview(next);
    announce(
      copy("agent-worker-wizard.sourceOrderposition", {
        position: next.indexOf(value.id) + 1,
        count: next.length,
      }),
    );
  };
  useLayoutEffect(() => {
    if (
      movement.current &&
      (!active ||
        !editable ||
        movement.current.authority !== transport ||
        JSON.stringify(movement.current.original) !== JSON.stringify(ids))
    )
      finish(false);
  }, [active, editable, transport, ids]);
  useLayoutEffect(() => {
    // Moving sibling nodes can drop DOM focus; keep it on the moving grip.
    const value = movement.current;
    if (value && document.activeElement === document.body) focus(value.id);
  }, [preview]);
  useLayoutEffect(() => {
    const cancel = () => finish(false);
    const abort = opening?.controller.signal;
    abort?.addEventListener("abort", cancel);
    const hide = () => {
      if (document.visibilityState === "hidden") cancel();
    };
    document.addEventListener("visibilitychange", hide);
    // Ancestor locks can change outside React props (native fieldset/modal ownership).
    const observer = new MutationObserver(() => {
      if (movement.current && !allowed()) cancel();
    });
    const ancestors: Element[] = [];
    for (
      let node: Element | null = root.current;
      node;
      node = node.parentElement
    )
      ancestors.push(node);
    ancestors.forEach((node) =>
      observer.observe(node, {
        attributes: true,
        attributeFilter: ["disabled", "hidden", "inert", "aria-disabled"],
      }),
    );
    return () => {
      movement.current = undefined;
      observer.disconnect();
      abort?.removeEventListener("abort", cancel);
      document.removeEventListener("visibilitychange", hide);
    };
  }, [opening, transport]);
  const shown = preview ?? ids;
  return (
    <>
      <ol
        ref={root}
        className="worker-source-order"
        onKeyDownCapture={(event) => {
          if (movement.current && event.key === "Escape") {
            event.preventDefault();
            event.stopPropagation();
            finish(false);
          }
        }}
      >
        {shown.map((id, index) => {
          const name =
            names.get(id) || copy("agent-worker-wizard.chooseSource");
          const moving = movement.current?.id === id;
          const grip = (
            <button
              type="button"
              className="worker-source-grip"
              ref={(node) => {
                if (node) handles.current.set(id, node);
                else handles.current.delete(id);
              }}
              disabled={!active || !editable || ids.length < 2}
              aria-label={copy("agent-worker-wizard.sourceOrderhandle", {
                position: index + 1,
                name: name || copy("agent-worker-wizard.chooseSource"),
              })}
              data-moving={movement.current?.id === id || undefined}
              onKeyDown={(event) => {
                if (
                  event.repeat ||
                  event.nativeEvent.isComposing ||
                  event.altKey ||
                  event.ctrlKey ||
                  event.metaKey ||
                  event.shiftKey
                )
                  return;
                if (event.key === " " || event.key === "Enter") {
                  event.preventDefault();
                  event.stopPropagation();
                  if (
                    movement.current?.id === id &&
                    movement.current.pointer === undefined
                  )
                    finish(true);
                  else begin(id);
                }
                if (
                  movement.current?.id === id &&
                  movement.current.pointer === undefined &&
                  ["ArrowUp", "ArrowDown"].includes(event.key)
                ) {
                  event.preventDefault();
                  event.stopPropagation();
                  move(index + (event.key === "ArrowUp" ? -1 : 1));
                  handles.current
                    .get(id)
                    ?.scrollIntoView?.({ block: "nearest" });
                }
              }}
              onPointerDown={(event) => {
                if (event.button !== 0) return;
                event.preventDefault();
                begin(id, event.pointerId);
                if (movement.current?.pointer === event.pointerId)
                  event.currentTarget.setPointerCapture?.(event.pointerId);
              }}
              onPointerMove={(event) => {
                const value = movement.current;
                if (!value || value.pointer !== event.pointerId) return;
                if (!allowed()) {
                  finish(false);
                  return;
                }
                const rows = Array.from(
                  root.current?.children ?? [],
                ) as HTMLElement[];
                const target = rows.findIndex(
                  (row) => event.clientY < row.getBoundingClientRect().bottom,
                );
                move(target < 0 ? rows.length - 1 : target);
                // Scroll the existing dialog body rather than creating a second scroll owner.
                for (
                  let node = root.current?.parentElement;
                  node;
                  node = node.parentElement
                ) {
                  if (
                    node.scrollHeight > node.clientHeight &&
                    /auto|scroll/.test(getComputedStyle(node).overflowY)
                  ) {
                    const bounds = node.getBoundingClientRect();
                    if (event.clientY < bounds.top + 40) node.scrollTop -= 24;
                    else if (event.clientY > bounds.bottom - 40)
                      node.scrollTop += 24;
                    break;
                  }
                }
              }}
              onPointerUp={(event) => {
                if (movement.current?.pointer === event.pointerId) finish(true);
              }}
              onPointerCancel={(event) => {
                if (movement.current?.pointer === event.pointerId)
                  finish(false);
              }}
              onLostPointerCapture={(event) => {
                if (movement.current?.pointer === event.pointerId)
                  finish(false);
              }}
            >
              <span aria-hidden="true">⠿</span>
            </button>
          );
          return (
            <li key={id} data-source-key={id} data-moving={moving || undefined}>
              {renderItem({ id, index, moving, grip })}
            </li>
          );
        })}
      </ol>
      <p
        role="status"
        aria-live="polite"
        className="worker-source-order-status"
      >
        {status}
      </p>
    </>
  );
}
