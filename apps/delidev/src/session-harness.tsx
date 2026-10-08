// SPDX-License-Identifier: Apache-2.0
import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { Harness } from "./configuration-fields";
import { document, object } from "./documents";
import { copy, useLocale } from "./localization";

const names: Record<Harness, string> = {
  [Harness.Codex]: "Codex", [Harness.Claude]: "Claude Code",
  [Harness.OpenCode]: "OpenCode", [Harness.Grok]: "Grok Build",
};

export function retainedSessionHarness(resource?: Resource): Harness | undefined {
  if (resource?.kind !== EntityKind.SESSION) return undefined;
  const data = document(resource);
  // A present but malformed initial execution must not borrow a fork identity.
  // Current execution and today's catalog are deliberately not presentation inputs.
  const configuration = Object.hasOwn(data, "initial_execution")
    ? object(object(data.initial_execution).configuration)
    : object(object(object(data.fork).snapshot).configuration);
  const harness = configuration.harness;
  return typeof harness === "string" && Object.hasOwn(names, harness) ? harness as Harness : undefined;
}

export function SessionHarness({ resource, children }: { resource?: Resource; children: React.ReactNode }) {
  useLocale();
  const harness = retainedSessionHarness(resource);
  return <div className="session-identity">
    {harness ? <span className={`session-harness-mark session-harness-mark-${harness}`} aria-hidden="true" /> : null}
    <div className="session-identity-text">
      <div className="session-harness-name">{harness ? names[harness] : copy("session.harnessUnavailable")}</div>
      {children}
    </div>
  </div>;
}
