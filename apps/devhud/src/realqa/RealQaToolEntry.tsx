import { invoke } from "@tauri-apps/api/core";
import { useState } from "react";

import { AuthFeature, useSession } from "../auth/SessionProvider";

type EntryStatus = "idle" | "opening" | "failed";

export function RealQaToolEntry({
  open = () => invoke<void>("show_realqa"),
}: {
  readonly open?: () => Promise<void>;
}) {
  const { failure, logout, ready, session, signIn } = useSession();
  const [status, setStatus] = useState<EntryStatus>("idle");
  const [authRequested, setAuthRequested] = useState(false);
  const authenticated = session.status === "prior-session-offline"
    || (session.status === "signed-in" && session.features.includes(AuthFeature.RealQa));

  const enter = async () => {
    setStatus("opening");
    try {
      await open();
      setStatus("idle");
    } catch {
      setStatus("failed");
    }
  };

  return (
    <article aria-labelledby="realqa-tool-title" className="tool-card">
      <div>
        <p className="eyebrow">Desktop · Internal</p>
        <h3 id="realqa-tool-title">RealQA</h3>
        <p>
          Capture, review, annotate, and submit screenshots as new GitHub issues.
        </p>
      </div>
      {!ready ? <p role="status">Checking the local account binding…</p> : null}
      {ready && !authenticated ? (
        <button
          className="primary-button"
          disabled={session.status === "authenticating"}
          onClick={() => {
            setAuthRequested(true);
            void signIn(AuthFeature.RealQa);
          }}
          type="button"
        >
          {session.status === "authenticating" ? "Signing in…" : "Sign in to RealQA"}
        </button>
      ) : null}
      {ready && authenticated ? (
        <div className="button-row">
          <button
            className="primary-button"
            disabled={status === "opening"}
            onClick={() => void enter()}
            type="button"
          >
            {status === "opening" ? "Opening RealQA…" : "Open RealQA"}
          </button>
          <button
            className="secondary-button"
            onClick={() => {
              setAuthRequested(true);
              void logout();
            }}
            type="button"
          >
            Log out
          </button>
        </div>
      ) : null}
      {session.status === "prior-session-offline" ? (
        <p className="muted" role="status">
          Offline access is limited to capture, editing, and encrypted drafts for
          the previously bound account.
        </p>
      ) : null}
      {failure && authRequested ? (
        <p className="error" role="alert">{failure.guidance}</p>
      ) : null}
      {status === "failed" ? (
        <p className="error" role="alert">
          RealQA could not open. Reauthenticate online or try again.
        </p>
      ) : null}
    </article>
  );
}
