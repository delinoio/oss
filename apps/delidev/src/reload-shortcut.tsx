// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef } from "react";
import {
  shortcutSnapshotVerified,
  useShortcutPreferences,
} from "./shortcut-preference-controller";
import { readOnlyShortcutSuppression } from "./shortcut-preferences";
import { shortcutCaptureListening } from "./shortcut-capture";
import {
  bindingMatches,
  globalShortcutBindings,
  ShortcutId,
  shortcutPlatform,
} from "./shortcuts";

const reloadProductWindow = () => window.location.reload();

/**
 * Window-level owner of the read-only primary+R "Reload page" action. It is
 * mounted above App (under the shortcut preference provider) so identity-read,
 * startup, connection and recovery screens receive it too. It listens in the
 * window capture phase so it runs before terminal hosts and product inputs, and
 * it stays out of the way of an active Settings shortcut capture. It never logs
 * key events or content.
 */
export function ReloadShortcutOwner({
  reload = reloadProductWindow,
}: {
  reload?: () => void;
}) {
  const { snapshot, ready } = useShortcutPreferences();
  const inactive = Boolean(
    readOnlyShortcutSuppression(
      ShortcutId.ReloadPage,
      snapshot.overrides,
      ready,
      shortcutSnapshotVerified(snapshot),
    ),
  );
  const state = useRef({ inactive, reload });
  state.current = { inactive, reload };
  useEffect(() => {
    const handle = (event: KeyboardEvent) => {
      if (
        state.current.inactive ||
        event.defaultPrevented ||
        event.isComposing ||
        event.keyCode === 229 ||
        event.repeat ||
        shortcutCaptureListening()
      )
        return;
      const platform = shortcutPlatform();
      if (
        !globalShortcutBindings[ShortcutId.ReloadPage].some((binding) =>
          bindingMatches(event, binding, platform),
        )
      )
        return;
      // Consume the chord before terminal hosts or product inputs receive it.
      event.preventDefault();
      event.stopImmediatePropagation();
      state.current.reload();
    };
    window.addEventListener("keydown", handle, true);
    return () => window.removeEventListener("keydown", handle, true);
  }, []);
  return null;
}
