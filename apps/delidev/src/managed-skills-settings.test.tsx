// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  EntityKind,
  ManagedSkillPackageSchema,
  ResourceSchema,
  ResourceService,
  SkillService,
  SystemCapability,
  SystemService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { ManagedSkillsSettings } from "./managed-skills-settings";
import { AgentManagedSkills } from "./agent-managed-skills";
import { MutationIntents } from "./mutation";
import { SettingsLifetime } from "./settings-lifetime";
import { SettingsTasks } from "./settings-task";
vi.mock("./configuration-fields", () => ({
  ResourceChoice: ({
    label,
    value,
    change,
    disabled,
  }: {
    label: string;
    value: string;
    change: (id: string) => void;
    disabled?: boolean;
  }) => (
    <label>
      {label}
      <select
        value={value}
        disabled={disabled}
        onChange={(event) => change(event.target.value)}
      >
        <option value="">Choose</option>
        <option value={machine}>Fixture Runner</option>
      </select>
    </label>
  ),
}));
const machine = newRequestId(),
  device = newRequestId(),
  agent = newRequestId();
const revision = "a".repeat(64);
function pkg(name: string, enabled: boolean, references: string[] = []) {
  return create(ManagedSkillPackageSchema, {
    selection: {
      workerDeviceId: device,
      packageId: newRequestId(),
      contentRevision: revision,
    },
    revision: 1n,
    name,
    description: `Literal <script>${name}</script>`,
    enabled,
    referencingAgentIds: references,
    supportedHarnessProfiles: ["codex"],
  });
}
async function fixture(options: { lost?: boolean; selection?: boolean } = {}) {
  const free = pkg("free-skill", true),
    disabled = pkg("disabled-skill", false),
    linked = pkg("linked-skill", true, [agent]);
  const wire = (packageId: string, enabled: boolean) => ({
    selection: {
      workerDeviceId: device,
      packageId,
      contentRevision: revision,
    },
    revision: 1n,
    name: free.name,
    description: free.description,
    enabled,
    supportedHarnessProfiles: ["codex"],
  });
  const imported = vi.fn((request: { requestId: string }) => {
    if (options.lost) throw new ConnectError("lost", Code.Unavailable);
    return {
      requestId: request.requestId,
      package: wire(request.requestId, true),
    };
  });
  const toggled = vi.fn(
    (request: {
      mutation?: { requestId: string; id: string };
      enabled: boolean;
    }) => ({
      requestId: request.mutation?.requestId ?? "",
      package: wire(request.mutation?.id ?? "", request.enabled),
    }),
  );
  const removed = vi.fn((request: { mutation?: { requestId: string } }) => ({
    requestId: request.mutation?.requestId,
  }));
  const change = vi.fn();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({ capabilities: [SystemCapability.MANAGED_SKILLS_V1] }),
    });
    router.service(ResourceService, {
      listResources: () => ({
        resources: [
          create(ResourceSchema, {
            id: agent,
            kind: EntityKind.AGENT,
            revision: 1n,
            schemaVersion: 4,
            documentJson: encode({
              name: "Review agent",
              harness: "codex",
              routes: [
                {
                  model: { provider_id: newRequestId(), native_id: "native" },
                  accounts: [{ id: "a" }],
                },
              ],
            }),
          }),
        ],
      }),
    });
    router.service(SkillService, {
      listManagedSkills: (request) => {
        expect(request.machineId).toBe(machine);
        return { packages: [free, disabled, linked] };
      },
      getManagedSkill: () => ({
        package: {
          selection: {
            workerDeviceId: device,
            packageId: free.selection!.packageId,
            contentRevision: revision,
          },
          revision: 1n,
          name: "free-skill",
          enabled: true,
          resources: [
            {
              path: "SKILL.md",
              size: 42n,
              mode: 0o644,
              digest: "b".repeat(64),
            },
          ],
        },
        skillMarkdown: "<script>literal only</script>",
      }),
      importManagedSkill: imported,
      setManagedSkillEnabled: toggled,
      deleteManagedSkill: removed,
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <SettingsLifetime>
          {() => (
            <MutationIntents>
              <SettingsTasks>
                {options.selection ? (
                  <AgentManagedSkills
                    data={{ harness: "codex", name: "Agent" }}
                    change={change}
                    active
                  />
                ) : (
                  <ManagedSkillsSettings active />
                )}
              </SettingsTasks>
            </MutationIntents>
          )}
        </SettingsLifetime>
      </QueryClientProvider>
    </TransportProvider>,
  );
  fireEvent.change(await screen.findByRole("combobox", { name: /Runner/ }), {
    target: { value: machine },
  });
  return { free, imported, toggled, removed, change };
}
it("shows Runner-scoped inventory, neutral disabled rows, named references and deletion guards", async () => {
  const f = await fixture();
  await screen.findByText("free-skill");
  const linked = screen.getByText("linked-skill").closest("article")!;
  expect(
    await within(linked).findByText(
      "Referenced by Agent Workers: Review agent",
    ),
  ).toBeTruthy();
  expect(
    (
      within(linked).getByRole("button", {
        name: /Delete/,
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(
    screen
      .getByText("disabled-skill")
      .closest("article")!
      .getAttribute("data-enabled"),
  ).toBe("false");
  expect(document.body.textContent).not.toContain(f.free.selection!.packageId);
  fireEvent.change(screen.getByRole("searchbox"), {
    target: { value: "missing" },
  });
  expect(screen.getByText("No matching skills.")).toBeTruthy();
});
it("views SKILL.md and the manifest as inert text", async () => {
  await fixture();
  await screen.findByText("free-skill");
  fireEvent.click(
    within(screen.getByText("free-skill").closest("article")!).getByRole(
      "button",
      { name: /View/ },
    ),
  );
  await screen.findByText("<script>literal only</script>");
  expect(document.querySelector("script")).toBeNull();
  expect(screen.getByText("SKILL.md", { selector: "code" })).toBeTruthy();
});
it("imports only the explicit directory on the selected Runner", async () => {
  const f = await fixture();
  await screen.findByText("free-skill");
  fireEvent.click(screen.getByRole("button", { name: "Import skill" }));
  fireEvent.change(
    await screen.findByLabelText(
      "Package directory on the selected Runner Device",
    ),
    { target: { value: "/worker/selected-package" } },
  );
  fireEvent.click(
    screen.getAllByRole("button", { name: "Import skill" }).at(-1)!,
  );
  await waitFor(() => expect(f.imported).toHaveBeenCalledOnce());
  expect(f.imported.mock.calls[0][0]).toMatchObject({
    machineId: machine,
    sourceDirectory: "/worker/selected-package",
  });
});
it("retains the original import request after response loss and offers explicit cleanup", async () => {
  const f = await fixture({ lost: true });
  await screen.findByText("free-skill");
  fireEvent.click(screen.getByRole("button", { name: "Import skill" }));
  fireEvent.change(
    await screen.findByLabelText(
      "Package directory on the selected Runner Device",
    ),
    { target: { value: "/worker/selected-package" } },
  );
  fireEvent.click(
    screen.getAllByRole("button", { name: "Import skill" }).at(-1)!,
  );
  await screen.findByRole("button", { name: "Retry original request" });
  fireEvent.click(
    screen.getByRole("button", { name: "Retry original request" }),
  );
  await waitFor(() => expect(f.imported).toHaveBeenCalledTimes(2));
  expect(f.imported.mock.calls[1][0]).toEqual(f.imported.mock.calls[0][0]);
  expect(
    screen.getByRole("button", { name: "Cancel unconfirmed import" }),
  ).toBeTruthy();
});
it("enables and deletes with revision-checked mutations", async () => {
  const f = await fixture();
  await screen.findByText("free-skill");
  fireEvent.click(
    within(screen.getByText("free-skill").closest("article")!).getByRole(
      "button",
      { name: /Disable/ },
    ),
  );
  await waitFor(() => expect(f.toggled).toHaveBeenCalledOnce());
  expect(f.toggled.mock.calls[0][0]).toMatchObject({
    machineId: machine,
    enabled: false,
    mutation: { id: f.free.selection!.packageId, expectedRevision: 1n },
  });
  fireEvent.click(
    within(screen.getByText("free-skill").closest("article")!).getByRole(
      "button",
      { name: /Delete/ },
    ),
  );
  expect(f.removed).not.toHaveBeenCalled();
  fireEvent.click(
    await screen.findByRole("button", { name: "Confirm deletion" }),
  );
  await waitFor(() => expect(f.removed).toHaveBeenCalledOnce());
});
it("writes explicit-presence Agent selections for eligible packages only", async () => {
  const f = await fixture({ selection: true });
  const add = await screen.findByLabelText("Add skill selection");
  expect(
    (
      screen.getByRole("option", {
        name: /disabled-skill/,
      }) as HTMLOptionElement
    ).disabled,
  ).toBe(true);
  fireEvent.change(add, { target: { value: f.free.selection!.packageId } });
  expect(f.change.mock.calls[0][0].managed_skills.selections).toEqual([
    {
      worker_device_id: device,
      package_id: f.free.selection!.packageId,
      content_revision: revision,
    },
  ]);
});
