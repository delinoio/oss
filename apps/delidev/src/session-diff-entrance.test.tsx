// SPDX-License-Identifier: Apache-2.0
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  ResourceService,
  SessionService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionDiff, type ComparisonDescriptor } from "./session-diff";
import { Comparison } from "./session-diff-model";
import {
  dispatchAnimation,
  setAnimation,
  stubReducedMotion,
} from "./session-tool-entrance-helpers";

function fixture() {
  const sessionId = newRequestId(),
    primary = newRequestId(),
    other = newRequestId();
  const reply = (value: object) => ({
    documentJson: encode({
      size: "0",
      binary: false,
      truncated: false,
      ...value,
    }),
  });
  const read = vi.fn(async (request: { queryJson: Uint8Array }) => {
    const q = JSON.parse(new TextDecoder().decode(request.queryJson));
    if (q.operation === "roots")
      return reply({
        roots: [
          { repository_id: other, name: "Other", primary: false },
          { repository_id: primary, name: "Primary", primary: true },
        ],
      });
    if (q.operation === "git-diff-options")
      return reply({
        diff_options: {
          version: 1,
          repository_id: q.repository_id,
          choices: [
            {
              reference: { type: "local-branch", name: "main" },
              available: true,
              configured: false,
            },
          ],
          default: { type: "local-branch", name: "main" },
        },
      });
    return reply({
      diff: {
        comparison: q.comparison,
        repository_id: q.repository_id,
        path: q.path,
        base: "commit",
        base_object: "a".repeat(40),
        head_commit: "a".repeat(40),
        patch: "+exact-diff-line\n",
        untracked: [],
        revision: "b".repeat(64),
        ...(q.comparison === "branch"
          ? {
              base_ref: q.base_ref,
              base_commit: "a".repeat(40),
              merge_base: "a".repeat(40),
            }
          : {}),
      },
    });
  });
  const transport = createRouterTransport((router) => {
    router.service(SessionService, {
      readSessionWorkspace: read,
      readSessionReviewContext: () => {
        throw new Error("unsupported context");
      },
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [] }),
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  let control!: {
    setOpen: (value: boolean) => void;
    setSelected: (value: ComparisonDescriptor | undefined) => void;
  };
  function View() {
    const [open, setOpen] = useState(true);
    const [selected, setSelected] = useState<ComparisonDescriptor>();
    control = { setOpen, setSelected };
    return (
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <MutationIntents>
            {open ? (
              <SessionDiff
                sessionId={sessionId}
                worktree
                close={() => setOpen(false)}
                selected={selected}
                openComparison={(value) => setSelected(value)}
              />
            ) : null}
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>
    );
  }
  return {
    View,
    primary,
    other,
    read,
    setOpen: (value: boolean) => act(() => control.setOpen(value)),
    setSelected: (value: ComparisonDescriptor | undefined) =>
      act(() => control.setSelected(value)),
  };
}

const header = () => document.querySelector<HTMLElement>("header.diff-navbar")!;
const entranceNodes = () => document.querySelectorAll("[data-entrance]");

beforeEach(() => {
  stubReducedMotion(false);
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.animation;
});

it("animates only the Diff header once per activation and never the diff body", async () => {
  const f = fixture();
  render(<f.View />);
  await screen.findByText("+exact-diff-line");
  const node = header();
  expect(node.getAttribute("data-entrance")).toBe("y2");
  expect(entranceNodes().length).toBe(1);
  expect(node.closest("[data-entrance]")).toBe(node);
  // The options dialog is never a descendant of the animated header.
  fireEvent.click(screen.getByRole("button", { name: "Diff options" }));
  expect(
    screen.getByRole("dialog", { name: "Diff options" }).closest("header"),
  ).toBeNull();
  dispatchAnimation(node);
  expect(entranceNodes().length).toBe(0);
});

it("does not replay for repository, path, comparison, refresh or tab changes and keeps scroll and reads", async () => {
  const f = fixture();
  render(<f.View />);
  await screen.findByText("+exact-diff-line");
  const aside = document.querySelector<HTMLElement>("aside.session-diff")!;
  aside.scrollTop = 37;
  const node = header();
  dispatchAnimation(node);
  fireEvent.click(screen.getByRole("button", { name: "Refresh diff" }));
  fireEvent.click(screen.getByRole("button", { name: "Diff options" }));
  fireEvent.change(screen.getByLabelText("Relative diff path"), {
    target: { value: "src/file.ts" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Compare path" }));
  // Moving to a Comparison tab and back reuses the same Diff instance.
  await waitFor(() => expect(f.read.mock.calls.length).toBeGreaterThan(2));
  f.setSelected({
    repository: f.primary,
    comparison: Comparison.Branch,
    path: "src/file.ts",
    base_ref: { type: "local-branch", name: "main" },
  } as ComparisonDescriptor);
  f.setSelected(undefined);
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.other },
  });
  await waitFor(() =>
    expect(
      screen.getByLabelText<HTMLSelectElement>("Diff repository").value,
    ).toBe(f.other),
  );
  await screen.findByText("+exact-diff-line");
  expect(entranceNodes().length).toBe(0);
  expect(aside.isConnected).toBe(true);
  expect(aside.scrollTop).toBe(37);
});

it("does not replay when the repository changes before the entrance finishes", async () => {
  const f = fixture();
  render(<f.View />);
  await screen.findByText("+exact-diff-line");
  const first = header();
  expect(first.getAttribute("data-entrance")).toBe("y2");
  // Change repositories while the initial entrance is still in flight.
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.other },
  });
  await waitFor(() =>
    expect(
      screen.getByLabelText<HTMLSelectElement>("Diff repository").value,
    ).toBe(f.other),
  );
  await screen.findByText("+exact-diff-line");
  // The keyed child remounted, so the replacement header is a new node.
  expect(header()).not.toBe(first);
  expect(entranceNodes().length).toBe(0);
  // Returning to the first repository inside the same visit never replays.
  fireEvent.change(screen.getByLabelText("Diff repository"), {
    target: { value: f.primary },
  });
  await waitFor(() =>
    expect(
      screen.getByLabelText<HTMLSelectElement>("Diff repository").value,
    ).toBe(f.primary),
  );
  await screen.findByText("+exact-diff-line");
  expect(entranceNodes().length).toBe(0);
  // A new open is a new activation and plays once again.
  f.setOpen(false);
  f.setOpen(true);
  await screen.findByText("+exact-diff-line");
  expect(entranceNodes().length).toBe(1);
});

it("replays only for a new open and leaves no stale attribute after rapid re-entry", async () => {
  const f = fixture();
  render(<f.View />);
  await screen.findByText("+exact-diff-line");
  expect(entranceNodes().length).toBe(1);
  f.setOpen(false);
  expect(entranceNodes().length).toBe(0);
  f.setOpen(true);
  expect(entranceNodes().length).toBe(1);
  dispatchAnimation(header());
  expect(entranceNodes().length).toBe(0);
});

it("is suppressed by Animation Off and reduced motion without replay on re-enable", async () => {
  const change = stubReducedMotion(false);
  const f = fixture();
  render(<f.View />);
  await screen.findByText("+exact-diff-line");
  expect(header().getAttribute("data-entrance")).toBe("y2");
  await setAnimation("false");
  expect(entranceNodes().length).toBe(0);
  await setAnimation(undefined);
  expect(entranceNodes().length).toBe(0);
  f.setOpen(false);
  f.setOpen(true);
  await screen.findByText("+exact-diff-line");
  expect(header().getAttribute("data-entrance")).toBe("y2");
  change(true);
  await waitFor(() => expect(entranceNodes().length).toBe(0));
  change(false);
  expect(entranceNodes().length).toBe(0);
});
