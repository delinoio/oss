import { useHandleSignInCallback } from "@logto/react";
import { useEffect, useRef } from "react";
import { useNavigate } from "react-router-dom";

import { consumeSignInReturnPath } from "../auth/AuthSession";
import { ErrorState, LoadingState } from "../components/States";

export function AuthCallbackPage() {
  const navigate = useNavigate();
  const callback = useHandleSignInCallback();
  const handled = useRef(false);

  useEffect(() => {
    if (!callback.isLoading && !handled.current) {
      handled.current = true;
      const returnTo = consumeSignInReturnPath();
      if (!callback.error && callback.isAuthenticated) {
        navigate(returnTo, { replace: true });
      }
    }
  }, [
    callback.error,
    callback.isAuthenticated,
    callback.isLoading,
    navigate,
  ]);

  return (
    <div className="page narrow">
      {callback.error ? (
        <ErrorState
          error={callback.error}
          title="We couldn’t complete sign-in"
        />
      ) : (
        <LoadingState label="Completing secure sign-in" />
      )}
    </div>
  );
}

export function UnavailableCallbackPage() {
  return (
    <div className="page narrow">
      <ErrorState
        error={new Error(
          "Logto is not configured for this build. No credentials were processed.",
        )}
        title="Sign-in unavailable"
      />
    </div>
  );
}
