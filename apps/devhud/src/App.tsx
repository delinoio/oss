import { useEffect, useState } from "react";

import {
  loadRuntimeInfo,
  tauriRuntimeBridge,
  type RuntimeInfo,
} from "./runtime/startup";

type ViewState =
  | { status: "loading" }
  | { status: "failed"; message: string }
  | { status: "ready"; runtimeInfo: RuntimeInfo };

export function App() {
  const [state, setState] = useState<ViewState>({ status: "loading" });

  useEffect(() => {
    let active = true;

    void loadRuntimeInfo(tauriRuntimeBridge).then(
      (runtimeInfo) => {
        if (active) {
          setState({ status: "ready", runtimeInfo });
        }
      },
      () => {
        if (active) {
          setState({
            status: "failed",
            message: "DevHud could not initialize its local runtime.",
          });
        }
      },
    );

    return () => {
      active = false;
    };
  }, []);

  return (
    <main>
      <p className="eyebrow">Local-only developer tool</p>
      <h1>DevHud</h1>
      {state.status === "loading" ? (
        <p role="status">Starting DevHud…</p>
      ) : null}
      {state.status === "failed" ? (
        <p role="alert">{state.message}</p>
      ) : null}
      {state.status === "ready" ? (
        <section aria-labelledby="runtime-status">
          <h2 id="runtime-status">DevHud is ready</h2>
          <dl>
            <div>
              <dt>Runtime</dt>
              <dd>{state.runtimeInfo.runtime}</dd>
            </div>
            <div>
              <dt>Bundled origin</dt>
              <dd>{state.runtimeInfo.bundledOrigin}</dd>
            </div>
            <div>
              <dt>CEF sandbox</dt>
              <dd>
                {state.runtimeInfo.sandboxEnabled ? "Enabled" : "Not applicable"}
              </dd>
            </div>
          </dl>
        </section>
      ) : null}
      <p className="scope">
        No tools are available in this foundation preview.
      </p>
    </main>
  );
}
