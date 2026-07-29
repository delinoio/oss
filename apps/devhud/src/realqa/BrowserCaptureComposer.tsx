import { useCallback, useEffect, useState } from "react";

import {
  takeBrowserCapture,
  type BrowserCapture,
} from "./browserCapture";

type ComposerState =
  | { readonly status: "loading" }
  | { readonly status: "empty" }
  | { readonly status: "failed" }
  | { readonly status: "ready"; readonly capture: BrowserCapture };

export function BrowserCaptureComposer() {
  const [state, setState] = useState<ComposerState>({ status: "loading" });
  const loadCapture = useCallback(async () => {
    try {
      const capture = await takeBrowserCapture();
      setState(
        capture === null
          ? { status: "empty" }
          : { status: "ready", capture },
      );
    } catch {
      setState({ status: "failed" });
    }
  }, []);

  useEffect(() => {
    const handleCapture = () => void loadCapture();
    window.addEventListener(
      "devhud:realqa-browser-capture-available",
      handleCapture,
    );
    const initialLoad = window.setTimeout(handleCapture, 0);
    return () => {
      window.clearTimeout(initialLoad);
      window.removeEventListener(
        "devhud:realqa-browser-capture-available",
        handleCapture,
      );
    };
  }, [loadCapture]);

  return (
    <main className="realqa-composer-shell">
      <header className="app-header">
        <div aria-label="RealQA" className="wordmark">
          <span aria-hidden="true">RQ</span>
          <strong>RealQA capture</strong>
        </div>
      </header>
      {state.status === "loading" ? (
        <p role="status">Receiving the browser capture…</p>
      ) : null}
      {state.status === "empty" ? (
        <section aria-labelledby="realqa-empty-title" className="state-card">
          <h1 id="realqa-empty-title">Waiting for a capture</h1>
          <p>Return to the RealQA Chrome extension and send a capture.</p>
        </section>
      ) : null}
      {state.status === "failed" ? (
        <section
          aria-labelledby="realqa-capture-error-title"
          className="state-card error-card"
        >
          <h1 id="realqa-capture-error-title">Capture unavailable</h1>
          <p role="alert">
            Sign in to RealQA on this device, then send the capture again.
          </p>
        </section>
      ) : null}
      {state.status === "ready" ? (
        <section aria-labelledby="realqa-capture-title" className="realqa-capture-card">
          <div>
            <p className="eyebrow">Browser handoff</p>
            <h1 id="realqa-capture-title">
              {state.capture.page?.title ?? "Untitled capture"}
            </h1>
            {state.capture.page?.url === undefined ? null : (
              <p className="muted">{state.capture.page.url}</p>
            )}
          </div>
          {state.capture.image === undefined ? (
            <p role="status">
              Chrome requested the native OS capture flow for this page.
            </p>
          ) : (
            <img
              alt="Captured browser viewport"
              className="realqa-capture-preview"
              src={`data:image/${state.capture.image.mediaType};base64,${state.capture.image.base64}`}
            />
          )}
          <button
            className="secondary-button"
            onClick={() => setState({ status: "empty" })}
            type="button"
          >
            Remove capture
          </button>
        </section>
      ) : null}
    </main>
  );
}
