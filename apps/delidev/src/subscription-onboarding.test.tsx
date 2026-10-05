// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SubscriptionOnboarding, SubscriptionOnboardingStage as Stage, subscriptionNameValid, type SubscriptionOnboardingProps } from "./subscription-onboarding";

function props(overrides: Partial<SubscriptionOnboardingProps> = {}): SubscriptionOnboardingProps {
  return { serviceName: "ChatGPT", stage: Stage.Waiting, active: true, name: "ChatGPT", suggested: false, busy: false, canReopen: true, canCancel: true, changeName: vi.fn(), saveName: vi.fn(), reopen: vi.fn(), cancel: vi.fn(), leave: vi.fn(), ...overrides };
}

it("renders browser waiting without starting login or exposing device or code controls", () => {
  const value = props();
  render(<StrictMode><SubscriptionOnboarding {...value} /></StrictMode>);
  expect(screen.getByRole("status").textContent).toContain("Waiting for ChatGPT sign-in");
  expect(screen.queryByLabelText("Account name")).toBeNull();
  expect(screen.queryByText(/Runner Device|Device code|Login address/)).toBeNull();
  expect(value.reopen).not.toHaveBeenCalled(); expect(value.cancel).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Open browser again" }));
  expect(value.reopen).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Cancel login" }));
  expect(value.cancel).toHaveBeenCalledTimes(1);
});

it("focuses the confirmed name once and preserves edits across progress rerenders", () => {
  const value = props();
  const view = render(<StrictMode><SubscriptionOnboarding {...value} /></StrictMode>);
  const named = { ...value, stage: Stage.Naming, name: "alex@example.com", suggested: true };
  view.rerender(<StrictMode><SubscriptionOnboarding {...named} /></StrictMode>);
  const input = screen.getByLabelText("Account name") as HTMLInputElement;
  expect(document.activeElement).toBe(input);
  fireEvent.change(input, { target: { value: "My account" } });
  expect(value.changeName).toHaveBeenCalledWith("My account");
  const later = screen.getByRole("button", { name: "Later" }); later.focus();
  view.rerender(<StrictMode><SubscriptionOnboarding {...named} name="My account" /></StrictMode>);
  expect(input.value).toBe("My account"); expect(document.activeElement).toBe(later);
  fireEvent.click(screen.getByRole("button", { name: "Save account name" }));
  expect(value.saveName).toHaveBeenCalledTimes(1);
  fireEvent.click(later); expect(value.leave).toHaveBeenCalledTimes(1); expect(value.cancel).not.toHaveBeenCalled();
});

it.each([Stage.Preparing, Stage.Canceled, Stage.Expired, Stage.Unsupported, Stage.Recovery, Stage.Failed])("provides readable %s progress without name or implicit retry", (stage) => {
  const value = props({ stage, canReopen: false, canCancel: false });
  render(<SubscriptionOnboarding {...value} />);
  expect(screen.getByRole("status").textContent?.length).toBeGreaterThan(0);
  expect(screen.queryByLabelText("Account name")).toBeNull();
  expect(screen.queryByRole("button", { name: "Open browser again" })).toBeNull();
  expect(value.reopen).not.toHaveBeenCalled();
});

it("keeps browser failure readable and explicit reopen separate from sign-in", () => {
  render(<SubscriptionOnboarding {...props({ problem: "The browser could not be opened. Open it again or cancel login." })} />);
  expect(screen.getByRole("alert").textContent).toContain("could not be opened");
  expect(screen.queryByText("Your browser has opened for sign-in. Return here after you finish.")).toBeNull();
  expect(screen.getByRole("button", { name: "Open browser again" })).toBeTruthy();
});

it("uses the existing UTF-8 name bound and rejects empty or NUL names", () => {
  expect(subscriptionNameValid(" ")).toBe(false); expect(subscriptionNameValid("account\0name")).toBe(false);
  expect(subscriptionNameValid("계".repeat(85))).toBe(true); expect(subscriptionNameValid("계".repeat(86))).toBe(false);
});
