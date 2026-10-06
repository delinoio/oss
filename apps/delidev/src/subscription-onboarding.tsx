// SPDX-License-Identifier: Apache-2.0
import { useSettingsTaskVisible, useCloseSettingsTask, useInSettingsTask } from "./settings-task-context";
import { SettingsTaskActions } from "./settings-task";
import { useEffect, useRef , useId } from "react";
import { CodexDiagnosticPhase, FailureCode, isEntityId, type CodexDiagnostic } from "@delinoio/delidev-api-client";
import "./subscription-onboarding.css";

export enum SubscriptionOnboardingStage {
  Preparing = "preparing",
  Waiting = "waiting",
  Naming = "naming",
  Canceled = "canceled",
  Expired = "expired",
  Unsupported = "unsupported",
  Recovery = "recovery",
  Failed = "failed",
}

export interface SubscriptionOnboardingProps {
  serviceName: string;
  stage: SubscriptionOnboardingStage;
  active: boolean;
  name: string;
  suggested: boolean;
  busy: boolean;
  problem?: string;
  diagnostic?: CodexDiagnostic;
  canReopen: boolean;
  canCancel: boolean;
  changeName: (name: string) => void;
  saveName: () => void;
  reopen: () => void;
  cancel: () => void;
  leave: () => void;
}

export function subscriptionNameValid(name: string): boolean {
  return name.trim().length > 0 && !name.includes("\0") && new TextEncoder().encode(name).byteLength <= 256;
}

// Presentation has no login side effects. The owning controller starts only
// from an explicit Add event and supplies the original operation's progress.
export function SubscriptionOnboarding(props: SubscriptionOnboardingProps) {
  const taskFormId = useId(), visible = useSettingsTaskVisible(), leave = useCloseSettingsTask(props.leave), inTask = useInSettingsTask();
  const { stage, serviceName, active, busy } = props;
  const naming = stage === SubscriptionOnboardingStage.Naming;
  const nameInput = useRef<HTMLInputElement>(null);
  const focused = useRef(false);
  useEffect(() => {
    if (visible && active && naming && !busy && !focused.current && nameInput.current) {
      focused.current = true;
      nameInput.current.focus({ preventScroll: true });
    }
  }, [active, naming, busy, visible]);
  const failed = serviceName === "ChatGPT" && [SubscriptionOnboardingStage.Unsupported, SubscriptionOnboardingStage.Failed, SubscriptionOnboardingStage.Recovery, SubscriptionOnboardingStage.Expired].includes(stage);
  const diagnostic = safeDiagnostic(props.diagnostic);
  const status = failed ? "ChatGPT sign-in failed" : {
    [SubscriptionOnboardingStage.Preparing]: `Preparing ${serviceName} sign-in`,
    [SubscriptionOnboardingStage.Waiting]: `Waiting for ${serviceName} sign-in`,
    [SubscriptionOnboardingStage.Naming]: `Signed in to ${serviceName}`,
    [SubscriptionOnboardingStage.Canceled]: "Login canceled",
    [SubscriptionOnboardingStage.Expired]: "Login expired",
    [SubscriptionOnboardingStage.Unsupported]: `${serviceName} sign-in is not supported yet`,
    [SubscriptionOnboardingStage.Recovery]: "The original login result requires recovery",
    [SubscriptionOnboardingStage.Failed]: "Login could not be completed",
  }[stage];
  return <section className="subscription-account-create subscription-onboarding" aria-label={`Add ${serviceName} account`}>
    <button type="button" className="subscription-onboarding-back" disabled={!active} onClick={leave}>Back to AI Subscription</button>
    <h2 hidden={inTask}>Add {serviceName} account</h2>
    <ol className="subscription-onboarding-steps" aria-label="Account setup progress">
      <li aria-current={!naming ? "step" : undefined}>{naming ? <span aria-hidden="true">✓ </span> : null}Sign in</li>
      <li aria-hidden="true">→</li>
      <li aria-current={naming ? "step" : undefined}>Account name</li>
    </ol>
    <p className={`subscription-onboarding-status${naming ? " subscription-onboarding-success" : ""}`} role="status">
      {stage === SubscriptionOnboardingStage.Preparing || stage === SubscriptionOnboardingStage.Waiting ? <span className="subscription-onboarding-spinner" aria-hidden="true" /> : null}
      {naming ? <span aria-hidden="true">✓</span> : null}{status}
    </p>
    {failed ? <div role="alert" className="subscription-onboarding-diagnostic">
      <p>{diagnostic?.message ?? props.problem ?? "The server did not report native failure details."}</p>
      <dl>
        <div><dt>Codex version:</dt><dd>{diagnostic ? diagnostic.version || "Not detected" : "Not reported"}</dd></div>
        <div><dt>Minimum version:</dt><dd>{diagnostic?.minimum ?? "Not reported"}</dd></div>
        <div><dt>Failed step:</dt><dd>{diagnostic?.phase ?? "Not reported"}</dd></div>
        <div><dt>Error code:</dt><dd>{diagnostic?.code ?? "Not reported"}</dd></div>
        {diagnostic?.correlation ? <div><dt>Reference:</dt><dd>{diagnostic.correlation}</dd></div> : null}
      </dl>
      {stage === SubscriptionOnboardingStage.Recovery ? <p>The original sign-in result requires recovery.</p> : null}
      <p>Check Connection &amp; diagnostics before starting another sign-in.</p>
    </div> : null}
    {naming ? <form id={`${taskFormId}-1`} onSubmit={(event) => { event.preventDefault(); if (active && !busy && subscriptionNameValid(props.name)) props.saveName(); }}>
      <label htmlFor="subscription-onboarding-name">Account name</label>
      <input id="subscription-onboarding-name" ref={nameInput} autoComplete="off" required value={props.name} disabled={!active || busy} onChange={(event) => props.changeName(event.target.value)} aria-describedby="subscription-onboarding-name-help" />
      <p id="subscription-onboarding-name-help">{props.suggested ? "Suggested from your signed-in account. You can change it." : "Choose a name for your signed-in account."}</p>
      <SettingsTaskActions form={`${taskFormId}-1`} className=""><button className="primary" disabled={!active || busy || !subscriptionNameValid(props.name)}>Save account name</button><button type="button" disabled={!active} onClick={leave}>Later</button></SettingsTaskActions>
    </form> : <>
      {stage === SubscriptionOnboardingStage.Waiting && !props.problem ? <p>Your browser has opened for sign-in. Return here after you finish.</p> : null}
      <SettingsTaskActions className="">
        {props.canReopen ? <button type="button" className="primary" disabled={!active || busy} onClick={props.reopen}>Open browser again</button> : null}
        {props.canCancel ? <button type="button" disabled={!active || busy} onClick={props.cancel}>Cancel login</button> : null}
      </SettingsTaskActions>
    </>}
    {props.problem && !failed ? <p role="alert">{props.problem}</p> : null}
    {(stage !== SubscriptionOnboardingStage.Unsupported || failed) ? <p className="subscription-onboarding-footer">{naming ? "Your signed-in account is kept if you leave this screen." : "Leaving this screen keeps the account and its current sign-in state."}</p> : null}
  </section>;
}

const phaseNames: Partial<Record<CodexDiagnosticPhase, string>> = {
 [CodexDiagnosticPhase.DISCOVERY]:"Executable discovery", [CodexDiagnosticPhase.VERSION]:"Version validation", [CodexDiagnosticPhase.PROFILE]:"Profile validation", [CodexDiagnosticPhase.RUNTIME]:"Runtime preparation", [CodexDiagnosticPhase.LAUNCH]:"Native launch", [CodexDiagnosticPhase.INITIALIZE]:"Initialization", [CodexDiagnosticPhase.CONFIRM]:"Initialization confirmation", [CodexDiagnosticPhase.LOGIN]:"Sign-in", [CodexDiagnosticPhase.MODELS]:"Model discovery", [CodexDiagnosticPhase.EXECUTION]:"Execution", [CodexDiagnosticPhase.HISTORY]:"History verification", [CodexDiagnosticPhase.CLEANUP]:"Cleanup",
};
const versionText = (value: string) => value.length <= 256 && /^[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?(?:\+[a-zA-Z0-9.-]+)?$/.test(value);
// Reconstruct text from closed metadata, never provider/native message strings.
function safeDiagnostic(d?: CodexDiagnostic) {
 if (!d || (d.detectedVersion && !versionText(d.detectedVersion)) || !versionText(d.minimumVersion) || !phaseNames[d.phase] || !Object.values(FailureCode).includes(d.code as FailureCode)) return;
 const reasons: Partial<Record<FailureCode,string>> = { [FailureCode.NotFound]: "The Codex executable was not found.", [FailureCode.Unsupported]: "The native protocol or version is incompatible.", [FailureCode.Unavailable]: "The native operation failed or timed out.", [FailureCode.Unauthenticated]: "Native authentication was not accepted.", [FailureCode.PermissionDenied]: "Native access was denied.", [FailureCode.RecoveryRequired]: "The native operation requires recovery." };
 const timeout = d.code === FailureCode.Unavailable && d.message === `Codex ${d.detectedVersion || "not detected"} did not complete ${phaseNames[d.phase]!.toLowerCase()}. The native operation timed out.`;
 const reason = timeout ? "The native operation timed out." : d.phase === CodexDiagnosticPhase.VERSION ? `Codex requires valid SemVer at or above ${d.minimumVersion}.` : reasons[d.code as FailureCode] ?? "The native operation did not complete.";
 return { version:d.detectedVersion, minimum:d.minimumVersion, phase:phaseNames[d.phase], code:d.code, correlation:isEntityId(d.correlationId) ? d.correlationId : "", message:`Codex ${d.detectedVersion || "not detected"} did not complete ${phaseNames[d.phase]!.toLowerCase()}. ${reason}` };
}
