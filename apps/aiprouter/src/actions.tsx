// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "./i18n";
import { useEffect, useRef, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQueryClient } from "@tanstack/react-query";
import { ErrorNotice } from "./ui";
import { useDirtyForm } from "./api";
export function useAction() {
  useLocale();
  const client = useQueryClient();
  const reportDirty = useDirtyForm();
  const mounted = useRef(true);
  const busy = useRef(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const [notice, setNotice] = useState("");
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  async function run<T>(
    operation: () => Promise<T>,
    done?: (value: T) => void,
    success = translate("changesSaved"),
    /**
     * Evaluated when the operation succeeds. Return false when the draft that
     * was submitted no longer owns the shell's unsaved-change flag, so a
     * completion that outlives its form cannot clear a newer draft's flag.
     */
    ownsDirty: () => boolean = () => true,
  ) {
    if (busy.current) return;
    busy.current = true;
    setPending(true);
    setError(undefined);
    setNotice("");
    try {
      const result = await operation();
      if (mounted.current) {
        if (ownsDirty()) reportDirty(false);
        setNotice(success);
        done?.(result);
        await client.invalidateQueries();
      }
    } catch (e) {
      if (mounted.current) {
        setError(e);
        const code = ConnectError.from(e).code;
        if (code === Code.PermissionDenied || code === Code.Unauthenticated)
          window.dispatchEvent(
            new CustomEvent("authority-changed", { detail: code }),
          );
      }
    } finally {
      busy.current = false;
      if (mounted.current) setPending(false);
    }
  }
  /** Clears the previous outcome; ignored while a request is in flight. */
  function reset() {
    if (busy.current) return;
    setError(undefined);
    setNotice("");
  }
  return {
    run,
    reset,
    pending,
    error,
    notice,
    feedback: (
      <>
        <ErrorNotice error={error} />
        <p className="mutation-status" role="status">
          {pending ? translate("message_c5ca36dfc7") : notice}
        </p>
      </>
    ),
  };
}

export type UseActionResult = ReturnType<typeof useAction>;
