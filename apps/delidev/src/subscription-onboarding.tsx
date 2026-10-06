import { LocalizedText, copy, useLocale } from "./localization";
import { useEffect, useRef } from "react";
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
  useLocale();
  const { stage, serviceName, active, busy } = props;
  const naming = stage === SubscriptionOnboardingStage.Naming;
  const nameInput = useRef<HTMLInputElement>(null);
  const focused = useRef(false);
  useEffect(() => {
    if (active && naming && !busy && !focused.current && nameInput.current) {
      focused.current = true;
      nameInput.current.focus({ preventScroll: true });
    }
  }, [active, naming, busy]);
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
  return <section className="subscription-account-create subscription-onboarding" aria-label={copy("subscription-onboarding.addAccount_8403fa", { v0: serviceName })}>
    <button type="button" className="subscription-onboarding-back" disabled={!active} onClick={props.leave}>{copy("subscription-onboarding.backToAiSubscription_224262")}</button>
    <h2><LocalizedText id="subscription-onboarding.addAccount_2c24d0" components={{ s0: <>{serviceName}</> }} /></h2>
    <ol className="subscription-onboarding-steps" aria-label={copy("subscription-onboarding.accountSetupProgress_269e63")}>
      <li aria-current={!naming ? "step" : undefined}><LocalizedText id="subscription-onboarding.signIn_b011b4" components={{ s0: <>{naming ? <span aria-hidden="true">✓ </span> : null}</> }} /></li>
      <li aria-hidden="true">→</li>
      <li aria-current={naming ? "step" : undefined}>{copy("subscription-onboarding.accountName_a704d8")}</li>
    </ol>
    <p className={`subscription-onboarding-status${naming ? " subscription-onboarding-success" : ""}`} role="status">
      {stage === SubscriptionOnboardingStage.Preparing || stage === SubscriptionOnboardingStage.Waiting ? <span className="subscription-onboarding-spinner" aria-hidden="true" /> : null}
      {naming ? <span aria-hidden="true">✓</span> : null}{status}
    </p>
    {failed ? <div role="alert" className="subscription-onboarding-diagnostic">
      <p>{diagnostic?.message ?? props.problem ?? copy("subscription-onboarding.theServerDidNotReportNative_923694")}</p>
      <dl>
        <div><dt>{copy("subscription-onboarding.codexVersion_072e4d")}</dt><dd>{diagnostic ? diagnostic.version || "Not detected" : copy("subscription-onboarding.notReported_adadfa")}</dd></div>
        <div><dt>{copy("subscription-onboarding.minimumVersion_3cab5a")}</dt><dd>{diagnostic?.minimum ?? copy("subscription-onboarding.notReported_adadfa")}</dd></div>
        <div><dt>{copy("subscription-onboarding.failedStep_0ed199")}</dt><dd>{diagnostic?.phase ?? copy("subscription-onboarding.notReported_adadfa")}</dd></div>
        <div><dt>{copy("subscription-onboarding.errorCode_2c35f6")}</dt><dd>{diagnostic?.code ?? copy("subscription-onboarding.notReported_adadfa")}</dd></div>
        {diagnostic?.correlation ? <div><dt>{copy("subscription-onboarding.reference_44dc4a")}</dt><dd>{diagnostic.correlation}</dd></div> : null}
      </dl>
      {stage === SubscriptionOnboardingStage.Recovery ? <p>{copy("subscription-onboarding.theOriginalSignInResultRequires_136770")}</p> : null}
      <p>{copy("subscription-onboarding.checkConnectionDiagnosticsBeforeStartingAnother_3836fd")}</p>
    </div> : null}
    {naming ? <form onSubmit={(event) => { event.preventDefault(); if (active && !busy && subscriptionNameValid(props.name)) props.saveName(); }}>
      <label htmlFor="subscription-onboarding-name">{copy("subscription-onboarding.accountName_a704d8")}</label>
      <input id="subscription-onboarding-name" ref={nameInput} autoComplete="off" required value={props.name} disabled={!active || busy} onChange={(event) => props.changeName(event.target.value)} aria-describedby="subscription-onboarding-name-help" />
      <p id="subscription-onboarding-name-help">{props.suggested ? copy("subscription-onboarding.suggestedFromYourSignedInAccount_5ddc0f") : copy("subscription-onboarding.chooseANameForYourSigned_6ec79b")}</p>
      <div className="actions"><button className="primary" disabled={!active || busy || !subscriptionNameValid(props.name)}>{copy("subscription-onboarding.saveAccountName_7c6744")}</button><button type="button" disabled={!active} onClick={props.leave}>{copy("subscription-onboarding.later_73b6e4")}</button></div>
    </form> : <>
      {stage === SubscriptionOnboardingStage.Waiting && !props.problem ? <p>{copy("subscription-onboarding.yourBrowserHasOpenedForSign_7b9723")}</p> : null}
      <div className="actions">
        {props.canReopen ? <button type="button" className="primary" disabled={!active || busy} onClick={props.reopen}>{copy("subscription-onboarding.openBrowserAgain_63833e")}</button> : null}
        {props.canCancel ? <button type="button" disabled={!active || busy} onClick={props.cancel}>{copy("subscription-onboarding.cancelLogin_8304c3")}</button> : null}
      </div>
    </>}
    {props.problem && !failed ? <p role="alert">{props.problem}</p> : null}
    {(stage !== SubscriptionOnboardingStage.Unsupported || failed) ? <p className="subscription-onboarding-footer">{naming ? copy("subscription-onboarding.yourSignedInAccountIsKept_1d2f83") : copy("subscription-onboarding.leavingThisScreenKeepsTheAccount_e209d9")}</p> : null}
  </section>;
}

const phaseNames: Partial<Record<CodexDiagnosticPhase, string>> = {
 get [CodexDiagnosticPhase.DISCOVERY]() { return copy("subscription-onboarding.executableDiscovery_774792"); }, get [CodexDiagnosticPhase.VERSION]() { return copy("subscription-onboarding.versionValidation_95b337"); }, get [CodexDiagnosticPhase.PROFILE]() { return copy("subscription-onboarding.profileValidation_b201db"); }, get [CodexDiagnosticPhase.RUNTIME]() { return copy("subscription-onboarding.runtimePreparation_2bdb24"); }, get [CodexDiagnosticPhase.LAUNCH]() { return copy("subscription-onboarding.nativeLaunch_4979af"); }, get [CodexDiagnosticPhase.INITIALIZE]() { return copy("subscription-onboarding.initialization_8be62f"); }, get [CodexDiagnosticPhase.CONFIRM]() { return copy("subscription-onboarding.initializationConfirmation_1e895c"); }, get [CodexDiagnosticPhase.LOGIN]() { return copy("subscription-onboarding.signIn_5bbbe5"); }, get [CodexDiagnosticPhase.MODELS]() { return copy("subscription-onboarding.modelDiscovery_3c49ba"); }, get [CodexDiagnosticPhase.EXECUTION]() { return copy("subscription-onboarding.execution_a45cd4"); }, get [CodexDiagnosticPhase.HISTORY]() { return copy("subscription-onboarding.historyVerification_97b291"); }, get [CodexDiagnosticPhase.CLEANUP]() { return copy("subscription-onboarding.cleanup_9f1b23"); },
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
