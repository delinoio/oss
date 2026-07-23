import { useMutation } from "@connectrpc/connect-query";
import { AccountService } from "@delinoio/delibase-connect";
import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";

import { useAuthSession } from "../auth/AuthSession";
import { OfflineActionHint } from "../components/States";
import { useDocumentMetadata } from "../hooks/useDocumentMetadata";
import { useOnline } from "../hooks/useOnline";
import { createIdempotencyKey } from "../utils/format";

const slugPattern = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

export function OnboardingPage() {
  useDocumentMetadata(
    "Create your organization",
    "Set up your DeliDev profile and first organization.",
  );
  const { transport } = useAuthSession();
  const navigate = useNavigate();
  const online = useOnline();
  const [displayName, setDisplayName] = useState("");
  const [organizationName, setOrganizationName] = useState("");
  const [organizationSlug, setOrganizationSlug] = useState("");
  const [formError, setFormError] = useState("");
  const complete = useMutation(AccountService.method.completeOnboarding, {
    transport,
  });

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError("");
    const normalizedSlug = organizationSlug.trim().toLowerCase();
    if (!displayName.trim() || !organizationName.trim()) {
      setFormError("Enter your name and organization name.");
      return;
    }
    if (!slugPattern.test(normalizedSlug)) {
      setFormError(
        "Use lowercase letters, numbers, and single hyphens for the slug.",
      );
      return;
    }
    complete.mutate(
      {
        displayName: displayName.trim(),
        idempotency: createIdempotencyKey(),
        organizationName: organizationName.trim(),
        organizationSlug: normalizedSlug,
      },
      {
        onError: (error) => setFormError(error.message),
        onSuccess: () => navigate(`/o/${normalizedSlug}/apps`, { replace: true }),
      },
    );
  };

  return (
    <div className="page narrow">
      <header className="page-heading">
        <span className="eyebrow">One quick setup</span>
        <h1>Create your workspace</h1>
        <p>
          This creates your profile, organization, and protected General team
          together.
        </p>
      </header>
      <form className="form-card" onSubmit={submit}>
        <label>
          Your name
          <input
            autoComplete="name"
            // This is the required first critical field in the onboarding flow.
            // eslint-disable-next-line jsx-a11y/no-autofocus
            autoFocus
            maxLength={120}
            onChange={(event) => setDisplayName(event.target.value)}
            required
            value={displayName}
          />
        </label>
        <label>
          Organization name
          <input
            autoComplete="organization"
            maxLength={120}
            onChange={(event) => setOrganizationName(event.target.value)}
            required
            value={organizationName}
          />
        </label>
        <label>
          Organization URL
          <span className="slug-input">
            <span aria-hidden="true">deli.dev/o/</span>
            <input
              aria-describedby="slug-help"
              autoCapitalize="none"
              autoComplete="off"
              maxLength={63}
              onChange={(event) => setOrganizationSlug(event.target.value)}
              pattern="[a-z0-9]+(?:-[a-z0-9]+)*"
              required
              spellCheck={false}
              value={organizationSlug}
            />
          </span>
          <span className="field-hint" id="slug-help">
            Lowercase letters, numbers, and hyphens. You can change it later.
          </span>
        </label>
        {formError ? (
          <p className="inline-error" role="alert">
            {formError}
          </p>
        ) : null}
        <button
          className="button primary full"
          disabled={!online || complete.isPending}
          type="submit"
        >
          {complete.isPending ? "Creating workspace…" : "Create workspace"}
        </button>
        {!online ? <OfflineActionHint /> : null}
      </form>
    </div>
  );
}
