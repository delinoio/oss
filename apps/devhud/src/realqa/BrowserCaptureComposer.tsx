import { useCallback, useEffect, useRef, useState } from "react";

import {
  takeBrowserCapture,
  type BrowserCapture,
  type BrowserDomSelection,
  type BrowserPageMetadata,
} from "./browserCapture";
import {
  subscribeToPersistenceReset,
  subscribeToSessionInvalidation,
} from "../runtime/theme";
import {
  createRealQaComposerBridge,
  ImageMediaType,
  type Base64EncodedImage,
  type ComposerImage,
  type RealQaBrowserComposerBridge,
} from "./capture";
import { ScreenshotEditor } from "./editor/ScreenshotEditor";

type ComposerState =
  | { readonly status: "loading" }
  | { readonly status: "empty" }
  | { readonly status: "locked" }
  | { readonly status: "failed" }
  | {
      readonly status: "os-capture";
      readonly imageId: string;
      readonly page?: BrowserPageMetadata;
    }
  | {
      readonly status: "os-capturing";
      readonly imageId: string;
      readonly page?: BrowserPageMetadata;
    }
  | {
      readonly status: "ready";
      readonly imageId: string;
      readonly page?: BrowserPageMetadata;
      readonly selection?: BrowserDomSelection;
      readonly source: ComposerImage;
    };

const browserSessionId = "realqa-browser-capture";
const defaultComposerBridge = createRealQaComposerBridge();

function encodedPngCapture(capture: BrowserCapture): Base64EncodedImage {
  if (capture.image?.mediaType !== "png") {
    throw new Error("The browser composer accepts PNG viewport captures only.");
  }
  return {
    mediaType: ImageMediaType.Png,
    base64: capture.image.base64,
  };
}

function DomSelectionSummary({
  selection,
}: {
  readonly selection: BrowserDomSelection;
}) {
  return (
    <section
      aria-labelledby="realqa-dom-selection-title"
      className="browser-selection"
    >
      <p className="eyebrow">DOM target</p>
      <h2 id="realqa-dom-selection-title">
        {selection.accessibleName ?? selection.tag ?? "Selected element"}
      </h2>
      <dl>
        {selection.selector === undefined ? null : (
          <div>
            <dt>Selector</dt>
            <dd>
              <code>{selection.selector}</code>
            </dd>
          </div>
        )}
        {selection.tag === undefined ? null : (
          <div>
            <dt>Element</dt>
            <dd>{selection.tag}</dd>
          </div>
        )}
        {selection.role === undefined ? null : (
          <div>
            <dt>Role</dt>
            <dd>{selection.role}</dd>
          </div>
        )}
        {selection.boundary === undefined ? null : (
          <div>
            <dt>Viewport boundary</dt>
            <dd>
              {selection.boundary.width} × {selection.boundary.height} at{" "}
              {selection.boundary.x}, {selection.boundary.y}
            </dd>
          </div>
        )}
      </dl>
    </section>
  );
}

export function BrowserCaptureComposer({
  composerBridge = defaultComposerBridge,
}: {
  readonly composerBridge?: RealQaBrowserComposerBridge;
}) {
  const [state, setState] = useState<ComposerState>({ status: "loading" });
  const captureDrain = useRef<Promise<void>>(Promise.resolve());
  const lifecycleRevision = useRef(0);
  const loadOneCapture = useCallback(async () => {
    const revision = lifecycleRevision.current;
    try {
      const capture = await takeBrowserCapture();
      if (revision !== lifecycleRevision.current) return;
      if (capture === null) {
        setState((current) =>
          current.status === "loading" ? { status: "empty" } : current,
        );
        return;
      }
      await composerBridge.resetSession(browserSessionId);
      if (revision !== lifecycleRevision.current) return;
      if (capture.image === undefined) {
        setState({
          status: "os-capture",
          imageId: capture.requestId,
          page: capture.page,
        });
        return;
      }
      const source = await composerBridge.acceptImage({
        sessionId: browserSessionId,
        imageId: capture.requestId,
        image: encodedPngCapture(capture),
        outputMediaType: ImageMediaType.Png,
      });
      if (revision !== lifecycleRevision.current) return;
      setState({
        status: "ready",
        imageId: capture.requestId,
        page: capture.page,
        selection: capture.selection,
        source,
      });
    } catch {
      if (revision === lifecycleRevision.current) {
        setState({ status: "failed" });
      }
    }
  }, [composerBridge]);
  const loadCapture = useCallback(() => {
    const nextDrain = captureDrain.current.then(loadOneCapture);
    captureDrain.current = nextDrain;
    return nextDrain;
  }, [loadOneCapture]);
  const startOsCapture = useCallback(
    ({
      imageId,
      page,
    }: {
      readonly imageId: string;
      readonly page?: BrowserPageMetadata;
    }) => {
      const revision = lifecycleRevision.current;
      setState({ status: "os-capturing", imageId, page });
      const nextDrain = captureDrain.current.then(async () => {
        try {
          const capture = await composerBridge.captureBrowserFallback(
            browserSessionId,
          );
          const source = await composerBridge.acceptImage({
            sessionId: browserSessionId,
            imageId,
            image: capture.image,
            outputMediaType: ImageMediaType.Png,
          });
          if (revision === lifecycleRevision.current) {
            setState({ status: "ready", imageId, page, source });
          }
        } catch {
          if (revision === lifecycleRevision.current) {
            setState({ status: "failed" });
          }
        }
      });
      captureDrain.current = nextDrain;
      return nextDrain;
    },
    [composerBridge],
  );

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

  useEffect(() => {
    const invalidate = () => {
      lifecycleRevision.current += 1;
      setState({ status: "locked" });
    };
    const unsubscribeReset = subscribeToPersistenceReset(invalidate);
    const unsubscribeSession = subscribeToSessionInvalidation(invalidate);
    return () => {
      unsubscribeReset();
      unsubscribeSession();
    };
  }, []);

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
      {state.status === "locked" ? (
        <section aria-labelledby="realqa-locked-title" className="state-card">
          <h1 id="realqa-locked-title">Capture locked</h1>
          <p role="alert">
            Sign in to RealQA on this device, then send the capture again.
          </p>
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
            Chrome cannot capture this page directly. Continue with a native
            capture of the primary display.
          </p>
          <button
            className="primary-button"
            onClick={() => void startOsCapture(state)}
            type="button"
          >
            Capture primary display
          </button>
        </section>
      ) : null}
      {state.status === "os-capturing" ? (
        <section aria-labelledby="realqa-capture-title" className="state-card">
          <h1 id="realqa-capture-title">
            {state.page?.title ?? "Native capture in progress"}
          </h1>
          <p role="status">
            Complete the operating system capture prompt to continue.
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
          {state.selection === undefined ? null : (
            <DomSelectionSummary selection={state.selection} />
          )}
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
              const revision = lifecycleRevision.current;
              const imageId = state.imageId;
              void composerBridge
                .removeImage(browserSessionId, imageId)
                .then(() => {
                  if (revision === lifecycleRevision.current) {
                    setState((current) =>
                      current.status === "ready" &&
                      current.imageId === imageId
                        ? { status: "empty" }
                        : current,
                    );
                  }
                })
                .catch(() => {
                  if (revision === lifecycleRevision.current) {
                    setState((current) =>
                      current.status === "ready" &&
                      current.imageId === imageId
                        ? { status: "failed" }
                        : current,
                    );
                  }
                });
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
