import { useEffect, useState, type ReactNode } from "react";
import { Link, NavLink } from "react-router-dom";

import {
  AuthStatus,
  useAuthSession,
} from "../auth/AuthSession";
import { useOnline } from "../hooks/useOnline";
import {
  activateWaitingServiceWorker,
  SERVICE_WORKER_UPDATE_EVENT,
} from "../pwa/register";

function Wordmark() {
  return (
    <Link className="wordmark" to="/" aria-label="DeliDev home">
      <span className="lettermark" aria-hidden="true">
        D
      </span>
      <span>DeliDev</span>
    </Link>
  );
}

export function AppFrame({ children }: { children: ReactNode }) {
  const auth = useAuthSession();
  const online = useOnline();
  const [updateReady, setUpdateReady] = useState(false);

  useEffect(() => {
    const announceUpdate = () => setUpdateReady(true);
    window.addEventListener(SERVICE_WORKER_UPDATE_EVENT, announceUpdate);
    return () =>
      window.removeEventListener(SERVICE_WORKER_UPDATE_EVENT, announceUpdate);
  }, []);

  return (
    <div className="app">
      <a className="skip-link" href="#main-content">
        Skip to main content
      </a>
      {!online ? (
        <div className="network-banner" role="status">
          You’re offline. Cached catalog pages remain available; account actions
          are paused.
        </div>
      ) : null}
      {updateReady ? (
        <div className="update-banner" role="status">
          <span>A new DeliDev version is ready.</span>
          <button
            type="button"
            onClick={() => void activateWaitingServiceWorker()}
          >
            Update now
          </button>
        </div>
      ) : null}
      <header className="site-header">
        <div className="header-inner">
          <Wordmark />
          <nav aria-label="Primary navigation">
            <NavLink to="/apps">Apps</NavLink>
            {auth.status === AuthStatus.SignedIn ? (
              <NavLink to="/account">Account</NavLink>
            ) : null}
          </nav>
          <div className="header-actions">
            {auth.status === AuthStatus.SignedIn ? (
              <button
                className="button quiet"
                type="button"
                onClick={() => void auth.signOut()}
              >
                Sign out
              </button>
            ) : (
              <button
                className="button primary compact"
                disabled={
                  !online ||
                  auth.status === AuthStatus.Loading ||
                  auth.status === AuthStatus.Unavailable
                }
                type="button"
                onClick={() => void auth.signIn()}
              >
                Sign in
              </button>
            )}
          </div>
        </div>
      </header>
      <main id="main-content" tabIndex={-1}>
        {children}
      </main>
      <footer className="site-footer">
        <Wordmark />
        <p>Developer tools, simply delivered.</p>
        <nav aria-label="Footer navigation">
          <Link to="/apps">Browse apps</Link>
          <a href="https://github.com/delinoio/oss">GitHub</a>
        </nav>
        <p className="legal">© {new Date().getFullYear()} DeliDev</p>
      </footer>
    </div>
  );
}
