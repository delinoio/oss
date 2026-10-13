// SPDX-License-Identifier: Apache-2.0
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import {
  EntityKind,
  PairDeviceRequestSchema,
  PairDeviceResponseSchema,
  ResourceSchema,
} from "@delinoio/delidev-api-client";
import { App } from "./app";
import { documentBytes, ProtectedState, uuid } from "./state";
import { en } from "./localization";
vi.mock("./platform", () => ({
  storage: { read: async () => null, write: async () => {} },
  observeForeground: async () => () => {},
  permission: async () => false,
  notify: async () => false,
  tls: async () => "ok",
}));
beforeEach(() => {
  // jsdom does not implement modal dialogs; emulate the open state.
  HTMLDialogElement.prototype.showModal = function showModal() {
    this.setAttribute("open", "");
  };
  HTMLDialogElement.prototype.close = function close() {
    this.removeAttribute("open");
  };
});
afterEach(cleanup);

const origin = "https://example.test";
const serverId = uuid();
const pairingId = uuid();
function grant(extra: Record<string, unknown> = {}): string {
  return JSON.stringify({
    version: 1,
    server_id: serverId,
    pairing_id: pairingId,
    endpoint: origin,
    code: "x".repeat(43),
    ...extra,
  });
}
function harness(respond: (body: Uint8Array) => Response | Promise<Response>) {
  const bodies: Uint8Array[] = [];
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    // After pairing succeeds the app connects normally; only count pairing.
    if (!String(input).includes("/PairDevice")) return lost();
    const body = init?.body as Uint8Array;
    bodies.push(new Uint8Array(body));
    return respond(body);
  });
  const state = new ProtectedState(
    { read: async () => null, write: async () => {} },
    fetcher as unknown as typeof fetch,
  );
  return { bodies, fetcher, state };
}
const lost = () => {
  throw new TypeError("lost");
};
// The form is busy (its fieldset disabled) until the in-flight run settles.
const settled = (dialog: HTMLElement) =>
  waitFor(() =>
    expect(dialog.querySelector("fieldset")?.disabled).toBe(false),
  );
async function openAdd(state: ProtectedState) {
  render(<App state={state} />);
  await screen.findByText(en.selectProfile);
  fireEvent.click(screen.getByRole("button", { name: en.settings }));
  await screen.findByRole("heading", { name: en.settings });
  fireEvent.click(screen.getByRole("button", { name: en.add }));
  return await screen.findByRole("dialog", { name: en.add });
}
function fill(dialog: HTMLElement, text: string) {
  fireEvent.change(within(dialog).getByLabelText(en.name), {
    target: { value: "Owned" },
  });
  fireEvent.change(within(dialog).getByLabelText(en.origin), {
    target: { value: origin },
  });
  fireEvent.change(within(dialog).getByLabelText(en.pairing), {
    target: { value: text },
  });
}

it("retries the retained original request when Pair is pressed again after an uncertain pair", async () => {
  const { bodies, fetcher, state } = harness(lost);
  const dialog = await openAdd(state);
  fill(dialog, grant());
  const prepare = vi.spyOn(state, "preparePair");
  const pair = vi.spyOn(state, "pair");
  fireEvent.click(within(dialog).getByRole("button", { name: en.pair }));
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
  const retry = await within(dialog).findByRole("button", {
    name: en.retryPair,
  });
  await settled(dialog);
  expect(state.state.profiles).toHaveLength(1);
  const id = state.state.profiles[0].id;
  // Inputs for the prepared attempt are frozen and the retry copy is shown.
  for (const label of [en.name, en.origin, en.pairing])
    expect(
      (within(dialog).getByLabelText(label) as HTMLInputElement).readOnly,
    ).toBe(true);
  expect(within(dialog).getByText(en.pendingPair)).toBeTruthy();
  fireEvent.click(retry);
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
  await settled(dialog);
  fireEvent.click(retry);
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(3));
  // preparePair ran exactly once; every pair call used the same profile.
  expect(prepare).toHaveBeenCalledTimes(1);
  expect(pair.mock.calls.map((call) => call[0])).toEqual([id, id, id]);
  expect(state.state.profiles).toHaveLength(1);
  const requests = bodies.map((body) =>
    fromBinary(PairDeviceRequestSchema, body),
  );
  expect(new Set(bodies.map((body) => Buffer.from(body).toString("hex"))).size)
    .toBe(1);
  expect(requests[0].requestId).toBe(state.state.profiles[0].pairing?.requestId);
  expect(requests[0].pairingId).toBe(pairingId);
  expect(requests[0].deviceId).toBe(state.state.profiles[0].deviceId);
  expect(state.state.profiles[0].pairing?.token).toBe(
    state.state.profiles[0].token,
  );
});

it("keeps the profile-list retry after dismissal and starts a separate creation for a new grant", async () => {
  const { fetcher, state } = harness(lost);
  const dialog = await openAdd(state);
  fill(dialog, grant());
  fireEvent.click(within(dialog).getByRole("button", { name: en.pair }));
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
  await within(dialog).findByRole("button", { name: en.retryPair });
  await settled(dialog);
  fireEvent.click(within(dialog).getByRole("button", { name: en.close }));
  expect(screen.queryByRole("dialog", { name: en.add })).toBeNull();
  // The protected profile survives dismissal and exposes the list retry.
  expect(state.state.profiles).toHaveLength(1);
  const listRetry = screen.getByRole("button", { name: en.retryPair });
  fireEvent.click(listRetry);
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy());
  // Reopening starts with empty, editable inputs and a fresh preparation.
  fireEvent.click(screen.getByRole("button", { name: en.add }));
  const again = await screen.findByRole("dialog", { name: en.add });
  for (const label of [en.name, en.origin, en.pairing]) {
    const input = within(again).getByLabelText(label) as HTMLInputElement;
    expect(input.readOnly).toBe(false);
    expect(input.value).toBe("");
  }
  fill(again, grant({ pairing_id: uuid() }));
  fireEvent.click(within(again).getByRole("button", { name: en.pair }));
  await waitFor(() => expect(state.state.profiles).toHaveLength(2));
});

it("retains nothing when preparePair itself fails", async () => {
  const { fetcher, state } = harness(lost);
  const dialog = await openAdd(state);
  fill(dialog, grant({ code: "short" }));
  const prepare = vi.spyOn(state, "preparePair");
  fireEvent.click(within(dialog).getByRole("button", { name: en.pair }));
  await screen.findByRole("alert");
  expect(state.state.profiles).toHaveLength(0);
  expect(fetcher).not.toHaveBeenCalled();
  expect(
    (within(dialog).getByLabelText(en.pairing) as HTMLTextAreaElement).readOnly,
  ).toBe(false);
  expect(within(dialog).queryByRole("button", { name: en.retryPair })).toBeNull();
  fireEvent.change(within(dialog).getByLabelText(en.pairing), {
    target: { value: grant() },
  });
  fireEvent.click(within(dialog).getByRole("button", { name: en.pair }));
  await waitFor(() => expect(prepare).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(state.state.profiles).toHaveLength(1));
});

it("clears the retained id and closes the modal after verified pairing", async () => {
  let fail = true;
  const { bodies, state } = harness((body) => {
    if (fail) return lost();
    const request = fromBinary(PairDeviceRequestSchema, body);
    return new Response(
      toBinary(
        PairDeviceResponseSchema,
        create(PairDeviceResponseSchema, {
          serverId,
          device: create(ResourceSchema, {
            kind: EntityKind.DEVICE,
            id: request.deviceId,
            revision: 1n,
            schemaVersion: 1,
            documentJson: documentBytes({ type: "client" }),
          }),
        }),
      ) as BodyInit,
      { status: 200, headers: { "content-type": "application/proto" } },
    );
  });
  const dialog = await openAdd(state);
  fill(dialog, grant());
  fireEvent.click(within(dialog).getByRole("button", { name: en.pair }));
  const retry = await within(dialog).findByRole("button", {
    name: en.retryPair,
  });
  await settled(dialog);
  fail = false;
  fireEvent.click(retry);
  await waitFor(() =>
    expect(screen.queryByRole("dialog", { name: en.add })).toBeNull(),
  );
  expect(bodies).toHaveLength(2);
  expect(state.state.profiles).toHaveLength(1);
  expect(state.state.profiles[0].pairing).toBeUndefined();
  fireEvent.click(screen.getByRole("button", { name: en.add }));
  const again = await screen.findByRole("dialog", { name: en.add });
  expect(
    (within(again).getByLabelText(en.pairing) as HTMLTextAreaElement).readOnly,
  ).toBe(false);
  expect(within(again).getByRole("button", { name: en.pair })).toBeTruthy();
});
