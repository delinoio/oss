// SPDX-License-Identifier: Apache-2.0
import { useEffect, useMemo, useRef, useState } from "react";
import { copy, useLocale } from "./localization";
import {
  ExternalLinkFailure,
  externalLinkFailure,
  externalLinkFailureMessages,
  externalLinksAvailable,
  openExternalUrl,
  splitLinkedText,
} from "./external-links";
import "./external-links.css";

/**
 * Preformatted user or assistant message text whose explicit HTTP(S) tokens are
 * activatable. The rendered text is byte-for-byte the input: links are only
 * wrappers around the same characters, so selection, copying and whitespace
 * are unchanged. Only message text uses this component; reasoning, tool, diff
 * and file evidence keep rendering inert text.
 */
export function LinkedPre({
  text,
  complete,
}: {
  text: string;
  complete: boolean;
}) {
  useLocale();
  const [failure, setFailure] = useState<ExternalLinkFailure>();
  const pending = useRef(false);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const native = externalLinksAvailable();
  const parts = useMemo(
    () => (native ? splitLinkedText(text, complete) : [{ text }]),
    [native, text, complete],
  );
  const open = async (url: string) => {
    // One activation is one launch: no retry and no second browser.
    if (pending.current) return;
    pending.current = true;
    setFailure(undefined);
    try {
      await openExternalUrl(url);
    } catch (error) {
      if (mounted.current) setFailure(externalLinkFailure(error));
    } finally {
      pending.current = false;
    }
  };
  return (
    <>
      <pre>
        {parts.map((part, index) =>
          "url" in part ? (
            <a
              key={index}
              className="external-link"
              href={part.url}
              rel="noreferrer noopener"
              onClick={(event) => {
                // Navigation is native-owned; the webview never follows it.
                event.preventDefault();
                void open(part.url);
              }}
              onAuxClick={(event) => event.preventDefault()}
            >
              {part.text}
            </a>
          ) : (
            part.text
          ),
        )}
      </pre>
      {failure ? (
        <p className="external-link-error" role="alert">
          <span>{copy(externalLinkFailureMessages[failure])}</span>
          <button
            type="button"
            aria-label={copy("external-link.dismiss")}
            onClick={() => setFailure(undefined)}
          >
            <svg aria-hidden="true" focusable="false" viewBox="0 0 24 24">
              <path d="M6 6l12 12M18 6 6 18" />
            </svg>
          </button>
        </p>
      ) : null}
    </>
  );
}
