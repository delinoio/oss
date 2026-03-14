import { type RefObject, useEffect } from "react";

export function useEscapeToClose(enabled: boolean, onClose: () => void): void {
  useEffect(() => {
    if (!enabled) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") {
        return;
      }

      event.preventDefault();
      event.stopPropagation();
      onClose();
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [enabled, onClose]);
}

export function useFocusOnShow<TElement extends HTMLElement>(
  enabled: boolean,
  elementRef: RefObject<TElement | null>,
): void {
  useEffect(() => {
    if (!enabled) return;

    const frameId = requestAnimationFrame(() => {
      elementRef.current?.focus();
    });

    return () => {
      cancelAnimationFrame(frameId);
    };
  }, [enabled, elementRef]);
}
