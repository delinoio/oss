import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  ExecutionState,
  LocalService,
  RunSchema,
} from "@delinoio/async-commit-hook-api-client";
import { afterEach, expect, it, vi } from "vitest";
import { Workspace } from "./App";
import { readConnection } from "./connection";

type RerunResult = {
  runId: string;
  startupDiagnostic?: { code: string; message: string; hint: string };
};

afterEach(() => {
  history.replaceState(null, "", "/");
});

function makeRun(id: string, parentId = "") {
  return create(RunSchema, {
    id,
    parentId,
    commit: `${id}-commit`,
    repositoryId: "repo",
    worktreeId: "tree",
    branch: "main",
    state: ExecutionState.FAILED,
  });
}

function renderWorkspace(
  rerun: () => RerunResult,
  initialRun = "source",
) {
  const transport = createRouterTransport((router) =>
    router.service(LocalService, {
      getVersion: () => ({ apiVersion: 1 }),
      listRepositories: () => ({
        repositories: [{
          id: "repo",
          name: "Repository",
          worktrees: [
            { id: "tree", path: "/repo", branch: "main" },
            { id: "other-tree", path: "/other", branch: "feature" },
          ],
        }],
      }),
      listBranches: () => ({
        branches: [
          { id: "main", name: "main" },
          { id: "feature", name: "feature" },
        ],
      }),
      listRuns: () => ({ runs: [makeRun("source")] }),
      getRun: ({ runId }: { runId: string }) => ({ run: makeRun(runId) }),
      rerun,
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const view = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <Workspace initialRun={initialRun} />
      </TransportProvider>
    </QueryClientProvider>,
  );
  return { ...view, client };
}

it("writes the accepted rerun to the refreshable fragment", async () => {
  history.replaceState(null, "", "/#run=source");
  const rerun = vi.fn(() => ({ runId: "accepted-run" }));
  const first = renderWorkspace(rerun, readConnection().run);
  try {
    fireEvent.click(await screen.findByRole("button", { name: "Rerun all checks" }));
    await waitFor(() => expect(location.hash).toBe("#run=accepted-run"));
    expect(await screen.findByRole("heading", { name: /accepted-run/ })).toBeTruthy();

    first.unmount();
    first.client.clear();
    const remounted = readConnection();
    expect(remounted).toEqual({ run: "accepted-run" });
    const second = renderWorkspace(rerun, remounted.run);
    try {
      expect(await screen.findByRole("heading", { name: /accepted-run/ })).toBeTruthy();
    } finally {
      second.unmount();
      second.client.clear();
    }
  } finally {
    first.unmount();
    first.client.clear();
  }
});

it("keeps the source fragment until a startup-failed rerun is opened", async () => {
  history.replaceState(null, "", "/#run=source");
  const rerun = vi.fn(() => ({
    runId: "accepted-run",
    startupDiagnostic: {
      code: "startup-failed",
      message: "The rerun was accepted, but the runner could not start.",
      hint: "Inspect ach doctor.",
    },
  }));
  const view = renderWorkspace(rerun, readConnection().run);
  try {
    fireEvent.click(await screen.findByRole("button", { name: "Rerun all checks" }));
    await screen.findByText("Rerun accepted; startup needs attention");
    expect(location.hash).toBe("#run=source");
    fireEvent.click(screen.getByRole("button", { name: "Open accepted execution" }));
    await waitFor(() => expect(location.hash).toBe("#run=accepted-run"));
  } finally {
    view.unmount();
    view.client.clear();
  }
});

it("clears the fragment when leaving detail or changing workspace filters", async () => {
  history.replaceState(null, "", "/#run=source");
  const view = renderWorkspace(() => ({ runId: "accepted-run" }), readConnection().run);
  try {
    fireEvent.click(await screen.findByRole("button", { name: "← All executions" }));
    await waitFor(() => expect(location.hash).toBe(""));

    const source = await screen.findByRole("button", { name: /source-commi/ });
    fireEvent.click(source);
    await waitFor(() => expect(location.hash).toBe("#run=source"));
    fireEvent.change(await screen.findByRole("combobox", { name: "Branch" }), {
      target: { value: "feature" },
    });
    await waitFor(() => expect(location.hash).toBe(""));

    fireEvent.click(await screen.findByRole("button", { name: /source-commi/ }));
    await waitFor(() => expect(location.hash).toBe("#run=source"));
    fireEvent.click(screen.getByRole("button", { name: "Changes" }));
    await waitFor(() => expect(location.hash).toBe(""));

    fireEvent.click(screen.getByRole("button", { name: "Checks" }));
    fireEvent.click(await screen.findByRole("button", { name: /source-commi/ }));
    await waitFor(() => expect(location.hash).toBe("#run=source"));
    fireEvent.click(screen.getByRole("button", { name: /\/other/ }));
    await waitFor(() => expect(location.hash).toBe(""));
  } finally {
    view.unmount();
    view.client.clear();
  }
});
