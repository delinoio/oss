import { useEffect, useState } from "react";

import {
  runBundledStartupHandshake,
  tauriProbeBridge,
  type StartupHandshake,
} from "./runtime/startup";

type ViewState =
  | { status: "checking" }
  | { status: "failed"; message: string }
  | { status: "passed"; handshake: StartupHandshake };

export function App() {
  const [state, setState] = useState<ViewState>({ status: "checking" });

  useEffect(() => {
    let active = true;

    void runBundledStartupHandshake(tauriProbeBridge).then(
      (handshake) => {
        if (active) {
          setState({ status: "passed", handshake });
        }
      },
      () => {
        if (active) {
          setState({
            status: "failed",
            message: "The bundled startup or capability-denial probe failed.",
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
      <p className="eyebrow">Local-only feasibility gate</p>
      <h1>DevHud bundled-asset probe</h1>
      {state.status === "checking" ? (
        <p role="status">Checking bundled startup and Tauri capability denial…</p>
      ) : null}
      {state.status === "failed" ? (
        <p role="alert">{state.message}</p>
      ) : null}
      {state.status === "passed" ? (
        <section aria-labelledby="probe-result">
          <h2 id="probe-result">Common startup probe passed</h2>
          <dl>
            <div>
              <dt>Runtime</dt>
              <dd>{state.handshake.receipt.runtime}</dd>
            </div>
            <div>
              <dt>Bundled origin</dt>
              <dd>{state.handshake.receipt.bundledOrigin}</dd>
            </div>
            <div>
              <dt>Capability denial</dt>
              <dd>{state.handshake.capabilityDenied ? "Observed" : "Missing"}</dd>
            </div>
          </dl>
        </section>
      ) : null}
      <p className="scope">
        This diagnostic shell contains no production developer tool and makes no
        network requests.
      </p>
    </main>
  );
}
