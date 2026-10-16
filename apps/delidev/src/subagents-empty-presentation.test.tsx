// SPDX-License-Identifier: Apache-2.0
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, test } from "vitest";
import { useState } from "react";
import {
  EntityKind,
  type Resource,
  ResourceService,
  SystemCapability,
  SystemService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Subagents } from "./subagents";
import { subagentFixture } from "./subagent-test-fixture";

interface Reply {
  resources?: Resource[];
  nextPageToken?: string;
  error?: ConnectError;
}
interface Held {
  sessionId: string;
  token: string;
  settle: (reply: Reply) => void;
}

/** Scripted ListResources server. Reads are answered immediately from `next`
 * unless `hold` is set, in which case they stay pending until settled. */
function harness(capabilities = [SystemCapability.SUBAGENT_OBSERVATION_V1]) {
  const state = {
    hold: false,
    next: { resources: [], nextPageToken: "" } as Reply,
    byToken: {} as Record<string, Reply>,
    held: [] as Held[],
    reads: [] as { sessionId: string; token: string }[],
  };
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities }) });
    router.service(ResourceService, {
      listResources: (request) => {
        const sessionId = request.filter?.sessionId ?? "";
        const token = request.filter?.pageToken ?? "";
        expect(request.filter?.kind).toBe(EntityKind.SUBAGENT);
        state.reads.push({ sessionId, token });
        const answer = (reply: Reply) => {
          if (reply.error) throw reply.error;
          return {
            resources: reply.resources ?? [],
            nextPageToken: reply.nextPageToken ?? "",
          };
        };
        if (!state.hold) return answer(state.byToken[token] ?? state.next);
        return new Promise((resolve, reject) => {
          state.held.push({
            sessionId,
            token,
            settle: (reply) => {
              try {
                resolve(answer(reply));
              } catch (error) {
                reject(error);
              }
            },
          });
        });
      },
    });
  });
  return { state, transport };
}

/** Mirrors the SessionView contract: the parent section is hidden exactly
 * when the controller reports a confirmed complete empty result. */
function Probe({
  sessionId,
  revision,
  published,
}: {
  sessionId: string;
  revision: string;
  published: boolean[];
}) {
  const [empty, setEmpty] = useState(false);
  return (
    <section data-testid="subagents-section" hidden={empty}>
      <Subagents
        sessionId={sessionId}
        revision={revision}
        emptyChanged={(value) => {
          published.push(value);
          setEmpty(value);
        }}
      />
    </section>
  );
}

function setup(sessionId: string, capabilities?: SystemCapability[]) {
  const h = harness(capabilities);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const published: boolean[] = [];
  const view = (revision: string, id = sessionId) => (
    <QueryClientProvider client={client}>
      <TransportProvider transport={h.transport}>
        <Probe sessionId={id} revision={revision} published={published} />
      </TransportProvider>
    </QueryClientProvider>
  );
  const hidden = () =>
    (screen.getAllByTestId("subagents-section").at(-1) as HTMLElement).hidden;
  return { ...h, client, published, view, hidden };
}

const settled = async (hidden: () => boolean) =>
  waitFor(() => expect(hidden()).toBe(true));

test("keeps a confirmed empty section hidden while repeated revisions refresh, including slow reads", async () => {
  const session = newRequestId();
  const t = setup(session);
  const mounted = render(t.view("1"));
  await settled(t.hidden);
  // Visibility changes after the first confirmed-empty report would flicker.
  const afterSettled = t.published.length;
  t.state.hold = true;
  for (const revision of ["2", "3", "4"]) {
    const reads = t.state.reads.length;
    mounted.rerender(t.view(revision));
    // The revision-triggered read is not suppressed by the hidden section.
    await waitFor(() => expect(t.state.reads.length).toBeGreaterThan(reads));
    expect(t.hidden()).toBe(true);
    await waitFor(() => expect(t.state.held.length).toBeGreaterThan(0));
    // A slow response keeps the same presentation until it completes.
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(t.hidden()).toBe(true);
    await act(async () => t.state.held.splice(0).forEach((h) => h.settle({})));
    await waitFor(() => expect(t.state.held).toHaveLength(0));
    expect(t.hidden()).toBe(true);
  }
  expect(t.published.slice(afterSettled).every(Boolean)).toBe(true);
  expect(screen.queryByText("Loading Subagents…")).toBeNull();
  t.client.clear();
});

test("reveals a child from an accepted refresh and hides again after a later complete empty result", async () => {
  const session = newRequestId();
  const t = setup(session);
  const mounted = render(t.view("1"));
  await settled(t.hidden);
  t.state.next = { resources: [subagentFixture(session)] };
  mounted.rerender(t.view("2"));
  await waitFor(() => expect(t.hidden()).toBe(false));
  await waitFor(() =>
    expect(
      screen.queryAllByRole("listitem"),
    ).toHaveLength(1),
  );
  t.state.next = {};
  mounted.rerender(t.view("3"));
  await settled(t.hidden);
  t.client.clear();
});

test("keeps a failed refresh and its pending Retry visible until a successful accepted result", async () => {
  const session = newRequestId();
  const t = setup(session);
  const mounted = render(t.view("1"));
  await settled(t.hidden);
  t.state.next = { error: new ConnectError("Fixture", Code.Unavailable) };
  mounted.rerender(t.view("2"));
  await screen.findByRole("button", { name: "Retry read" });
  expect(t.hidden()).toBe(false);
  // The error clears while Retry is pending; the failure latch keeps the
  // section visible.
  t.state.hold = true;
  fireEvent.click(screen.getByRole("button", { name: "Retry read" }));
  await waitFor(() => expect(t.state.held).toHaveLength(1));
  expect(t.hidden()).toBe(false);
  await act(async () => t.state.held.splice(0).forEach((h) => h.settle({})));
  await settled(t.hidden);
  t.client.clear();
});

test("keeps a failed refresh visible while an explicit Reload-class recovery is pending", async () => {
  const session = newRequestId();
  const t = setup(session);
  const mounted = render(t.view("1"));
  await settled(t.hidden);
  t.state.next = { error: new ConnectError("Fixture", Code.Unavailable) };
  mounted.rerender(t.view("2"));
  await screen.findByRole("button", { name: "Retry read" });
  t.state.next = {};
  t.state.hold = true;
  fireEvent.click(screen.getByRole("button", { name: "Retry read" }));
  await waitFor(() => expect(t.state.held).toHaveLength(1));
  expect(t.published.at(-1)).toBe(false);
  expect(t.hidden()).toBe(false);
  t.state.held
    .splice(0)
    .forEach((h) =>
      h.settle({ error: new ConnectError("Again", Code.Unavailable) }),
    );
  await screen.findByRole("button", { name: "Retry read" });
  expect(t.hidden()).toBe(false);
  t.client.clear();
});

test("never treats initial, unsupported, continued or malformed reads as confirmed empty", async () => {
  // Initial read held pending.
  const initial = setup(newRequestId());
  initial.state.hold = true;
  const first = render(initial.view("1"));
  await waitFor(() => expect(initial.state.held).toHaveLength(1));
  expect(initial.hidden()).toBe(false);
  expect(initial.published.includes(true)).toBe(false);
  first.unmount();
  initial.client.clear();

  // Missing capability.
  const unsupported = setup(newRequestId(), []);
  const mountedUnsupported = render(unsupported.view("1"));
  await screen.findByText(
    "This server does not support child-agent observations.",
  );
  expect(unsupported.hidden()).toBe(false);
  expect(unsupported.published.includes(true)).toBe(false);
  expect(unsupported.state.reads).toHaveLength(0);
  unsupported.client.clear();
  mountedUnsupported.unmount();

  // Continuation token on an otherwise empty page.
  const continued = setup(newRequestId());
  continued.state.next = { nextPageToken: "original-next" };
  const third = render(continued.view("1"));
  await screen.findByRole("button", { name: "Load more Subagents" });
  continued.state.hold = true;
  third.rerender(continued.view("2"));
  await waitFor(() => expect(continued.state.held).toHaveLength(1));
  expect(continued.hidden()).toBe(false);
  expect(continued.published.includes(true)).toBe(false);
  continued.client.clear();
  third.unmount();

  // Foreign child page is rejected, not accepted as empty.
  const session = newRequestId();
  const malformed = setup(session);
  malformed.state.next = { resources: [subagentFixture(newRequestId())] };
  render(malformed.view("1"));
  await screen.findByRole("button", { name: "Retry read" });
  expect(malformed.hidden()).toBe(false);
  expect(malformed.published.includes(true)).toBe(false);
  malformed.client.clear();
});

test("resets presentation when the session owner changes and ignores a stale old response", async () => {
  const first = newRequestId(),
    second = newRequestId();
  const t = setup(first);
  const mounted = render(t.view("1"));
  await settled(t.hidden);
  t.state.hold = true;
  mounted.rerender(t.view("2"));
  await waitFor(() => expect(t.state.held).toHaveLength(1));
  const stale = t.state.held.splice(0);
  expect(stale[0].sessionId).toBe(first);
  // The new owner starts visible, and its own initial read stays pending.
  mounted.rerender(t.view("1", second));
  await waitFor(() => expect(t.state.held).toHaveLength(1));
  expect(t.state.held[0].sessionId).toBe(second);
  expect(t.hidden()).toBe(false);
  // The old owner's delayed empty response cannot hide the new owner.
  await act(async () => stale.forEach((h) => h.settle({})));
  expect(t.hidden()).toBe(false);
  await act(async () => t.state.held.splice(0).forEach((h) => h.settle({})));
  await settled(t.hidden);
  t.client.clear();
});

test("keeps Subagents visible after a continuation chain ends in an empty terminal page", async () => {
  const t = setup(newRequestId());
  // The first accepted page is empty but carries a continuation token.
  t.state.next = { nextPageToken: "second" };
  t.state.byToken.second = {};
  render(t.view("1"));
  fireEvent.click(
    await screen.findByRole("button", { name: "Load more Subagents" }),
  );
  // The Additional read completes with no rows and no further token, so the
  // chain holds two pages and zero rows but is not a single-page snapshot.
  await waitFor(() =>
    expect(t.state.reads.map((read) => read.token)).toContain("second"),
  );
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "Load more Subagents" }),
    ).toBeNull(),
  );
  expect(t.hidden()).toBe(false);
  expect(t.published.includes(true)).toBe(false);
  t.client.clear();
});

test("resets presentation when a replacement transport keeps the same QueryClient and session", async () => {
  const session = newRequestId();
  const old = harness();
  const replacement = harness();
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const published: boolean[] = [];
  const view = (transport: typeof old.transport, revision: string) => (
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <Probe sessionId={session} revision={revision} published={published} />
      </TransportProvider>
    </QueryClientProvider>
  );
  const hidden = () =>
    (screen.getAllByTestId("subagents-section").at(-1) as HTMLElement).hidden;
  const mounted = render(view(old.transport, "1"));
  await settled(hidden);
  // The replacement owner's first read stays pending: the old owner's empty
  // snapshot is no evidence for it, so the section must be visible.
  replacement.state.hold = true;
  mounted.rerender(view(replacement.transport, "1"));
  await waitFor(() => expect(replacement.state.held).toHaveLength(1));
  expect(hidden()).toBe(false);
  expect(published.at(-1)).toBe(false);
  // Only the replacement owner's accepted empty result hides it again.
  await act(async () =>
    replacement.state.held.splice(0).forEach((h) => h.settle({})),
  );
  await settled(hidden);
  client.clear();
});
