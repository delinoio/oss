import { useCallback, useEffect, useState } from "react";

import {
  takeBrowserCapture,
  type BrowserCapture,
  type BrowserPageMetadata,
} from "./browserCapture";
import {
  createRealQaComposerBridge,
  ImageMediaType,
  type ComposerImage,
  type RealQaComposerBridge,
} from "./capture";
import { ScreenshotEditor } from "./editor/ScreenshotEditor";

type ComposerState =
  | { readonly status: "loading" }
  | { readonly status: "empty" }
  | { readonly status: "failed" }
  | {
      readonly status: "os-capture";
      readonly page?: BrowserPageMetadata;
    }
  | {
      readonly status: "ready";
      readonly imageId: string;
      readonly page?: BrowserPageMetadata;
      readonly source: ComposerImage;
    };

const browserSessionId = "realqa-browser-capture";
const defaultComposerBridge = createRealQaComposerBridge();

function decodePngCapture(capture: BrowserCapture): readonly number[] {
  if (capture.image?.mediaType !== "png") {
    throw new Error("The browser composer accepts PNG viewport captures only.");
  }
  return Array.from(atob(capture.image.base64), (character) =>
    character.charCodeAt(0),
  );
}

export function BrowserCaptureComposer({
  composerBridge = defaultComposerBridge,
}: {
  readonly composerBridge?: RealQaComposerBridge;
}) {
  const [state, setState] = useState<ComposerState>({ status: "loading" });
  const loadCapture = useCallback(async () => {
    try {
      const capture = await takeBrowserCapture();
      if (capture === null) {
        setState({ status: "empty" });
        return;
      }
      await composerBridge.resetSession(browserSessionId);
      if (capture.image === undefined) {
        setState({ status: "os-capture", page: capture.page });
        return;
      }
      const source = await composerBridge.acceptImage({
        sessionId: browserSessionId,
        imageId: capture.requestId,
        image: {
          mediaType: ImageMediaType.Png,
          bytes: decodePngCapture(capture),
        },
        outputMediaType: ImageMediaType.Png,
      });
      setState({
        status: "ready",
        imageId: capture.requestId,
        page: capture.page,
        source,
      });
    } catch {
      setState({ status: "failed" });
    }
  }, [composerBridge]);

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
      {state.status === "os-capture" ? (
        <section aria-labelledby="realqa-capture-title" className="state-card">
          <h1 id="realqa-capture-title">
            {state.page?.title ?? "Native capture requested"}
          </h1>
          <p role="status">
            Chrome requested the native OS capture flow for this page.
          </p>
        </section>
      ) : null}
      {state.status === "ready" ? (
        <section
          aria-labelledby="realqa-capture-title"
          className="realqa-capture-card"
        >
          <div>
            <p className="eyebrow">Browser handoff</p>
            <h1 id="realqa-capture-title">
              {state.page?.title ?? "Untitled capture"}
            </h1>
            {state.page?.url === undefined ? null : (
              <p className="muted">{state.page.url}</p>
            )}
          </div>
          <ScreenshotEditor.Provider
            bridge={composerBridge}
            imageId={state.imageId}
            onApprove={() => undefined}
            sessionId={browserSessionId}
            source={state.source}
          >
            <ScreenshotEditor.Frame>
              <ScreenshotEditor.Toolbar />
              <ScreenshotEditor.Canvas />
              <ScreenshotEditor.Inspector />
              <ScreenshotEditor.Actions />
            </ScreenshotEditor.Frame>
          </ScreenshotEditor.Provider>
          <button
            className="secondary-button"
            onClick={() => {
              void composerBridge
                .removeImage(browserSessionId, state.imageId)
                .then(() => setState({ status: "empty" }))
                .catch(() => setState({ status: "failed" }));
            }}
            type="button"
          >
            Remove capture
          </button>
        </section>
      ) : null}
    </main>
  );
}
