// SPDX-License-Identifier: Apache-2.0
import { createClient } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { EntityKind, ResourceService } from "@delinoio/delidev-api-client";
import { Settings, SettingsEntryDestination } from "./settings";
import { document } from "./documents";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();

it("registers a URL and connects a project on a real server without any Worker or folder", async () => {
  const resources = createClient(ResourceService, fixture.transport);
  expect((await resources.listResources({ filter: { kind: EntityKind.MACHINE } })).resources).toHaveLength(0);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<TransportProvider transport={fixture.transport}><QueryClientProvider client={client}><Settings entryDestination={SettingsEntryDestination.Repositories} /></QueryClientProvider></TransportProvider>);
  fireEvent.click(await screen.findByRole("button", { name: "Add repository" }));
  const dialog = screen.getByRole("dialog", { name: "Add repository" });
  fireEvent.change(within(dialog).getByLabelText("Git URL"), { target: { value: "git@github.com:fixture/remote-only.git" } });
  const add = within(dialog).getByRole("button", { name: "Add repository" });
  await waitFor(() => expect((add as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(add);
  await screen.findByRole("heading", { name: "remote-only" });
  const repositories = (await resources.listResources({ filter: { kind: EntityKind.REPOSITORY } })).resources;
  expect(repositories).toHaveLength(1);
  expect(document(repositories[0])).toMatchObject({ remote_url: "git@github.com:fixture/remote-only.git", checkouts: [] });
  fireEvent.click(screen.getByRole("button", { name: "Projects" }));
  fireEvent.click(screen.getByRole("button", { name: "New Project" }));
  fireEvent.click(await screen.findByRole("checkbox", { name: "remote-only" }));
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  fireEvent.change(screen.getByLabelText("Project name", { exact: true }), { target: { value: "Remote project" } });
  fireEvent.change(screen.getByLabelText("Primary repository", { exact: true }), { target: { value: repositories[0].id } });
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  await screen.findByRole("heading", { name: "Remote project" });
  const projects = (await resources.listResources({ filter: { kind: EntityKind.PROJECT } })).resources;
  expect(document(projects[0])).toMatchObject({ repositories: [repositories[0].id], primary_repository: repositories[0].id });
  expect((await resources.listResources({ filter: { kind: EntityKind.MACHINE } })).resources).toHaveLength(0);
}, 30000);
