// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQueryClient } from "@tanstack/react-query";
import { ErrorNotice } from "./ui";
import { useDirtyForm } from "./api";
export function useAction() {
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
    success = "Changes saved.",
  ) {
    if (busy.current) return;
    busy.current = true;
    setPending(true);
    setError(undefined);
    setNotice("");
    try {
      const result = await operation();
      if (mounted.current) {
        reportDirty(false);
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
  return {
    run,
    pending,
    error,
    feedback: (
      <>
        <ErrorNotice error={error} />
        <p className="mutation-status" role="status">
          {pending
            ? "Saving… Closing this dialog does not cancel a submitted change."
            : notice}
        </p>
      </>
    ),
  };
}
