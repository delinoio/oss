// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { RepositoryEditCompletion } from "./repository-edit-completion";
import { JobState } from "./jobs";
import { i18n, resolveMessage, SupportedLanguage } from "./localization";

const fixture = vi.hoisted(() => ({ notify: vi.fn(), opening: { disposed: false } }));
vi.mock("./toast-notifications", async importOriginal => ({ ...await importOriginal<typeof import("./toast-notifications")>(), useNotifications: () => ({ notify: fixture.notify }) }));
vi.mock("./settings-lifetime", async importOriginal => ({ ...await importOriginal<typeof import("./settings-lifetime")>(), useSettingsOpening: () => fixture.opening }));
beforeEach(() => { fixture.notify.mockReset(); fixture.opening.disposed = false; });
const id = "01900000-0000-7000-8000-000000000001", jobId = "01900000-0000-7000-8000-000000000002";
function props() { return { jobId, repositoryId: id, submittedId: id, expectedRevision: 1n, state: JobState.Succeeded, output: { id, revision: "2" }, observation: { verified: true, pending: false, retry: vi.fn() }, active: true, saved: vi.fn() }; }

it("completes once under Strict Mode and repeated observations with the original job toast identity", () => {
  const value = props();
  const view = render(<StrictMode><RepositoryEditCompletion {...value} /></StrictMode>);
  view.rerender(<StrictMode><RepositoryEditCompletion {...value} output={{ id, revision: "3" }} /></StrictMode>);
  expect(value.saved).toHaveBeenCalledTimes(1);
  expect(fixture.notify).toHaveBeenCalledTimes(1);
  expect(fixture.notify).toHaveBeenCalledWith(expect.objectContaining({ id: jobId, kind: "success" }));
  expect(screen.queryByRole("button", { name: "Done" })).toBeNull();
});

it.each([
  { output: {} }, { output: { id: jobId, revision: "2" } }, { output: { id, revision: "1" } },
  { output: { id, revision: "invalid" } }, { output: { id, revision: 9007199254740992 } },
  { submittedId: jobId }, { expectedRevision: undefined },
  { observation: { verified: false, pending: false, retry: vi.fn() } },
])("keeps unverified original completion available for status-read retry (%j)", patch => {
  const value = { ...props(), ...patch };
  render(<RepositoryEditCompletion {...value} />);
  expect(value.saved).not.toHaveBeenCalled(); expect(fixture.notify).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Retry original status read" }));
  expect(value.observation.retry).toHaveBeenCalledTimes(1);
  expect(value.saved).not.toHaveBeenCalled();
});

it.each([JobState.Queued, JobState.Claimed, JobState.Uncertain, JobState.Failed, JobState.Canceled])("never completes %s from an acknowledgment or fallback", state => {
  const value = props(); render(<RepositoryEditCompletion {...value} state={state} />);
  expect(value.saved).not.toHaveBeenCalled(); expect(fixture.notify).not.toHaveBeenCalled();
});

it("retains pending guidance and completes only after a verified retry with exact large revisions", () => {
  const value = props();
  const view = render(<RepositoryEditCompletion {...value} expectedRevision={9007199254740992n} output={{ id, revision: "9007199254740993" }} observation={{ ...value.observation, verified: false, pending: true }} />);
  expect((screen.getByRole("button", { name: "Retry original status read" }) as HTMLButtonElement).disabled).toBe(true);
  expect(value.saved).not.toHaveBeenCalled();
  view.rerender(<RepositoryEditCompletion {...value} expectedRevision={9007199254740992n} output={{ id, revision: "9007199254740993" }} />);
  expect(value.saved).toHaveBeenCalledTimes(1);
});

it("fences inactive or disposed openings and never completes an unmounted predecessor", () => {
  const value = props();
  const view = render(<RepositoryEditCompletion {...value} active={false} />);
  fixture.opening.disposed = true;
  view.rerender(<RepositoryEditCompletion {...value} active />);
  expect(value.saved).not.toHaveBeenCalled(); expect(fixture.notify).not.toHaveBeenCalled();
  view.unmount(); fixture.opening.disposed = false;
  const successor = props(); render(<RepositoryEditCompletion {...successor} state={JobState.Queued} />);
  expect(successor.saved).not.toHaveBeenCalled(); expect(fixture.notify).not.toHaveBeenCalled();
});

it.each([SupportedLanguage.English, SupportedLanguage.Korean])("uses existing localized completion copy in %s", async language => {
  await act(async () => { await i18n.changeLanguage(language); });
  try {
    render(<RepositoryEditCompletion {...props()} />);
    const message = resolveMessage(fixture.notify.mock.calls[0][0].message);
    expect(message).toBe(language === SupportedLanguage.Korean ? "저장소를 저장했습니다." : "Repository saved.");
  } finally { await act(async () => { await i18n.changeLanguage(SupportedLanguage.English); }); }
});
