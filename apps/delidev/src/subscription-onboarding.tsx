// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef } from "react";
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
  const status = {
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
    <button type="button" className="subscription-onboarding-back" disabled={!active} onClick={props.leave}>Back to AI Subscription</button>
    <h2>Add {serviceName} account</h2>
    <ol className="subscription-onboarding-steps" aria-label="Account setup progress">
      <li aria-current={!naming ? "step" : undefined}>{naming ? <span aria-hidden="true">✓ </span> : null}Sign in</li>
      <li aria-hidden="true">→</li>
      <li aria-current={naming ? "step" : undefined}>Account name</li>
    </ol>
    <p className={`subscription-onboarding-status${naming ? " subscription-onboarding-success" : ""}`} role="status">
      {stage === SubscriptionOnboardingStage.Preparing || stage === SubscriptionOnboardingStage.Waiting ? <span className="subscription-onboarding-spinner" aria-hidden="true" /> : null}
      {naming ? <span aria-hidden="true">✓</span> : null}{status}
    </p>
    {naming ? <form onSubmit={(event) => { event.preventDefault(); if (active && !busy && subscriptionNameValid(props.name)) props.saveName(); }}>
      <label htmlFor="subscription-onboarding-name">Account name</label>
      <input id="subscription-onboarding-name" ref={nameInput} autoComplete="off" required value={props.name} disabled={!active || busy} onChange={(event) => props.changeName(event.target.value)} aria-describedby="subscription-onboarding-name-help" />
      <p id="subscription-onboarding-name-help">{props.suggested ? "Suggested from your signed-in account. You can change it." : "Choose a name for your signed-in account."}</p>
      <div className="actions"><button className="primary" disabled={!active || busy || !subscriptionNameValid(props.name)}>Save account name</button><button type="button" disabled={!active} onClick={props.leave}>Later</button></div>
    </form> : <>
      {stage === SubscriptionOnboardingStage.Waiting && !props.problem ? <p>Your browser has opened for sign-in. Return here after you finish.</p> : null}
      <div className="actions">
        {props.canReopen ? <button type="button" className="primary" disabled={!active || busy} onClick={props.reopen}>Open browser again</button> : null}
        {props.canCancel ? <button type="button" disabled={!active || busy} onClick={props.cancel}>Cancel login</button> : null}
      </div>
    </>}
    {props.problem ? <p role="alert">{props.problem}</p> : null}
    {stage !== SubscriptionOnboardingStage.Unsupported ? <p className="subscription-onboarding-footer">{naming ? "Your signed-in account is kept if you leave this screen." : "Leaving this screen keeps the account and its current sign-in state."}</p> : null}
  </section>;
}
