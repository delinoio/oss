// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import {
  ConfigurationService,
  EntityKind,
  ResourceSchema,
  ResourceService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { ProjectSettingsMenu } from "./project-settings-menu";
import { SessionRowActionsProvider } from "./session-row-actions";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
beforeEach(() =>
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      disconnect() {}
      unobserve() {}
    },
  ),
);
function mount(
  active = true,
  saveConfiguration: (request: {
    id: string;
    documentJson: Uint8Array;
    schemaVersion: number;
  }) => unknown = () => {
    throw new ConnectError("unexpected save", Code.Internal);
  },
) {
  const ids = [newRequestId(), newRequestId()],
    repositoryId = newRequestId(),
    repository = create(ResourceSchema, {
      id: repositoryId,
      kind: EntityKind.REPOSITORY,
      revision: 1n,
      schemaVersion: 1,
      documentJson: encode({
        name: "Repository",
        remote_url: "https://github.com/delinoio/oss.git",
      }),
    }),
    reads: string[] = [],
    toggle = vi.fn(),
    createSession = vi.fn();
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, {
      getResource: (request) => {
        if (request.id === repositoryId) return { resource: repository };
        reads.push(request.id);
        return {
          resource: create(ResourceSchema, {
            id: request.id,
            kind: EntityKind.PROJECT,
            revision: 1n,
            schemaVersion: 1,
            documentJson: encode({
              name: "Repeated",
              repositories: [repositoryId],
              primary_repository: repositoryId,
              defaults: {},
            }),
          }),
        };
      },
      listResources: (request) => ({
        resources:
          request.filter?.kind === EntityKind.REPOSITORY ? [repository] : [],
      }),
    });
    router.service(ConfigurationService, {
      saveConfiguration: saveConfiguration as never,
    });
  });
  render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <MutationIntents>
          <SessionRowActionsProvider active={active}>
            {ids.map((id) => (
              <section key={id} className="sidebar-project-group">
                <button
                  className="sidebar-project-row"
                  aria-expanded="false"
                  onClick={toggle}
                >
                  Repeated
                </button>
                <button onClick={createSession}>+</button>
                <ProjectSettingsMenu
                  projectId={id}
                  label="Repeated"
                  active={active}
                />
              </section>
            ))}
          </SessionRowActionsProvider>
        </MutationIntents>
      </TransportProvider>
    </QueryClientProvider>,
  );
  return { ids, reads, toggle, createSession, client };
}
it("menu navigation and dismissal do not read, expand or select projects", () => {
  const { reads, toggle, createSession } = mount();
  const trigger = screen.getAllByRole("button", { name: /Project actions/ })[0];
  fireEvent.click(trigger);
  expect(screen.getAllByRole("menuitem")).toHaveLength(1);
  const item = screen.getByRole("menuitem", { name: "Project settings" });
  expect(document.activeElement).toBe(item);
  fireEvent.keyDown(item, { key: "End" });
  expect(document.activeElement).toBe(item);
  fireEvent.keyDown(item, { key: "Escape" });
  expect(screen.queryByRole("menu")).toBeNull();
  expect(document.activeElement).toBe(trigger);
  fireEvent.click(trigger);
  fireEvent.pointerDown(document.body);
  expect(screen.queryByRole("menu")).toBeNull();
  expect(reads).toEqual([]);
  expect(toggle).not.toHaveBeenCalled();
  expect(createSession).not.toHaveBeenCalled();
});
it("duplicate labels keep original project identity and durable dialog opener", async () => {
  const { ids, reads, client } = mount();
  const trigger = screen.getAllByRole("button", { name: /Project actions/ })[1];
  fireEvent.click(trigger);
  fireEvent.click(screen.getByRole("menuitem", { name: "Project settings" }));
  await waitFor(() => expect(reads).toEqual([ids[1]]));
  expect(screen.queryByRole("menu")).toBeNull();
  const name = await screen.findByRole("textbox", { name: "Name" });
  fireEvent.change(name, { target: { value: "Retained draft" } });
  await act(async () => {
    await client.invalidateQueries();
  });
  expect((name as HTMLInputElement).value).toBe("Retained draft");
  const dialog = screen.getByRole("dialog", { name: "Project settings" });
  fireEvent(dialog, new Event("cancel", { bubbles: false, cancelable: true }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(document.activeElement).toBe(trigger);
});
it("inactive triggers cannot open menus or perform reads", () => {
  const { reads } = mount(false);
  const trigger = screen.getAllByRole("button", { name: /Project actions/ })[0];
  expect((trigger as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(trigger);
  expect(screen.queryByRole("menu")).toBeNull();
  expect(reads).toEqual([]);
});

async function openAndRename(name: string): Promise<HTMLElement> {
  const trigger = screen.getAllByRole("button", { name: /Project actions/ })[0];
  fireEvent.click(trigger);
  fireEvent.click(screen.getByRole("menuitem", { name: "Project settings" }));
  const field = await screen.findByRole("textbox", { name: "Name" });
  fireEvent.change(field, { target: { value: name } });
  fireEvent.click(screen.getByRole("button", { name: /^Save/ }));
  return trigger;
}
it("refreshes active navigation queries after a successful save and restores focus", async () => {
  const save = vi.fn((request: { id: string; schemaVersion: number }) => ({
    resource: create(ResourceSchema, {
      id: request.id,
      kind: EntityKind.PROJECT,
      revision: 2n,
      schemaVersion: request.schemaVersion,
      documentJson: encode({
        name: "Renamed",
        repositories: [],
        defaults: {},
      }),
    }),
  }));
  const { client } = mount(true, save);
  const invalidate = vi.spyOn(client, "invalidateQueries");
  const trigger = await openAndRename("Renamed");
  await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(invalidate).toHaveBeenCalledWith({ refetchType: "active" });
  expect(document.activeElement).toBe(trigger);
});
it("does not invalidate navigation or close the dialog when a save fails", async () => {
  const save = vi.fn(() => {
    throw new ConnectError("rejected", Code.FailedPrecondition);
  });
  const { client } = mount(true, save);
  const invalidate = vi.spyOn(client, "invalidateQueries");
  await openAndRename("Renamed");
  await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
  await act(async () => {
    await Promise.resolve();
  });
  expect(screen.getByRole("dialog", { name: "Project settings" })).toBeTruthy();
  expect(invalidate).not.toHaveBeenCalled();
});
