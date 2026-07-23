import { useHandleSignInCallback } from "@logto/react";
import { useEffect } from "react";
import { useNavigate } from "react-router-dom";

import { safeReturnPath } from "../auth/AuthSession";
import { ErrorState, LoadingState } from "../components/States";

export function AuthCallbackPage() {
  const navigate = useNavigate();
  const callback = useHandleSignInCallback();

  useEffect(() => {
    if (!callback.isLoading && callback.isAuthenticated && !callback.error) {
      const returnTo = safeReturnPath(
        sessionStorage.getItem("delidev:return-to"),
      );
      sessionStorage.removeItem("delidev:return-to");
      navigate(returnTo, { replace: true });
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
