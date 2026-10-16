// SPDX-License-Identifier: Apache-2.0
import { execFile, spawn } from "node:child_process";
import { readFile, realpath } from "node:fs/promises";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { createClient } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it } from "vitest";
import {
  type GetSessionBudgetResponse,
  BudgetState,
  SessionService,
  ConfigurationService,
  EntityKind,
  ResourceService,
  ScheduleService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { SessionBudget } from "./session-budget";
import { Schedules } from "./schedules";
import { NewSession, NewSessionKind } from "./new-session";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { document, encode } from "./documents";
import { copy } from "./localization";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();

async function choose(control: HTMLElement, name: string | RegExp) {
  // The inline list is its own always-visible choice container.
  const inline = control.getAttribute("role") === "listbox";
  await waitFor(() =>
    expect(
      inline
        ? control.getAttribute("aria-disabled") === "true"
        : control.matches(":disabled"),
    ).toBe(false),
  );
  if (!inline) fireEvent.click(control);
  const popup = inline
    ? control
    : window.document.getElementById(control.getAttribute("aria-controls")!)!;
  const option = await within(popup).findByRole("option", { name });
  const id = option.dataset.pickerId;
  fireEvent.click(option);
  await waitFor(() => expect(control.dataset.value).toBe(id));
}

it("inspects and saves a real owned Git checkout through a separate Go Worker before creating a project", async () => {
  const { directory, transport, providerOrigin, binary, scope, runCLI } =
    fixture;
  const pairing = JSON.parse(
    await runCLI([
      "device",
      "create-pairing",
      "--type",
      "worker",
      "--name",
      "Owned Git Worker",
    ]),
  );
  const grant = await readFile(pairing.result.code_file, "utf8");
  const workerRoot = join(directory, "worker");
  await runCLI(
    [
      "worker",
      "pair",
      "--worker-dir",
      workerRoot,
      "--name",
      "Owned Git Worker",
      "--code-stdin",
    ],
    grant,
  );
  fixture.worker = spawn(
    binary,
    ["--data-dir", scope, "worker", "start", "--worker-dir", workerRoot],
    { stdio: ["ignore", "pipe", "ignore"] },
  );
  await new Promise<void>((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error("Owned Worker readiness timed out")),
      10000,
    );
    let output = "";
    const fail = () => {
      clearTimeout(timer);
      reject(new Error("Owned Worker exited before readiness"));
    };
    fixture.worker!.once("error", fail);
    fixture.worker!.once("exit", fail);
    fixture.worker!.stdout!.on("data", (chunk: Buffer) => {
      output += chunk.toString();
      if (output.length > 16384) {
        fail();
        return;
      }
      if (output.includes('"status":"ready"')) {
        clearTimeout(timer);
        fixture.worker!.removeListener("exit", fail);
        fixture.worker!.removeListener("error", fail);
        resolve();
      }
    });
  });
  const checkout = join(directory, "checkout");
  await promisify(execFile)("git", ["init", "--quiet", checkout], {
    timeout: 10000,
  });
  await promisify(execFile)(
    "git",
    [
      "-C",
      checkout,
      "remote",
      "add",
      "origin",
      "https://github.com/delinoio/oss.git",
    ],
    { timeout: 10000 },
  );
  const canonical = await realpath(checkout);
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false, gcTime: 0 },
    },
  });
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <Settings />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  const change = (name: string, value: string) =>
    fireEvent.change(screen.getByLabelText(name, { exact: true }), {
      target: { value },
    });
  fireEvent.click(screen.getByRole("button", { name: "Repositories" }));
  fireEvent.click(screen.getByRole("button", { name: "Add repository" }));
  change("Git URL", "https://github.com/delinoio/oss.git");
  fireEvent.click(
    screen.getByRole("button", { name: "Connect a Local folder (optional)" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Enter a path…" }));
  change("Computer", "remote");
  await choose(
    screen.getByRole("combobox", { name: "Runner Device" }),
    "Owned Git Worker",
  );
  change("Absolute checkout path", checkout);
  fireEvent.click(screen.getByRole("button", { name: "Inspect folder" }));
  await screen.findByRole(
    "region",
    { name: "Repository detected" },
    { timeout: 15000 },
  );
  fireEvent.click(screen.getByRole("button", { name: "Optional settings" }));
  change("Repository name", "Owned repository");
  fireEvent.click(
    within(screen.getByRole("dialog", { name: "Add repository" })).getByRole(
      "button",
      { name: "Add repository" },
    ),
  );
  await waitFor(
    () =>
      expect(
        screen.queryByRole("region", { name: "Add repository" }),
      ).toBeNull(),
    { timeout: 15000 },
  );
  await screen.findByRole("heading", { name: "Owned repository" });
  const repositories = await createClient(
    ResourceService,
    transport,
  ).listResources({ filter: { kind: EntityKind.REPOSITORY } });
  expect(document(repositories.resources[0]).checkouts).toEqual([
    expect.objectContaining({ path: canonical }),
  ]);
  expect(document(repositories.resources[0])).toMatchObject({
    github_owner: "delinoio",
    github_name: "oss",
    base: {},
    starting: {},
    auto_fetch: true,
  });
  expect(document(repositories.resources[0]).integration_id).toBeUndefined();
  fireEvent.click(screen.getByRole("button", { name: "Projects" }));
  fireEvent.click(screen.getByRole("button", { name: "New Project" }));
  fireEvent.click(
    await screen.findByRole("checkbox", { name: "Owned repository" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  change("Project name", "Owned project");
  change("Primary repository", repositories.resources[0].id);
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Restrict ai accounts" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  await screen.findByRole("heading", { name: "Owned project" });
  const projects = await createClient(ResourceService, transport).listResources(
    { filter: { kind: EntityKind.PROJECT } },
  );
  expect(document(projects.resources[0])).toMatchObject({
    primary_repository: repositories.resources[0].id,
    accounts: { configured: true, ids: [] },
  });
  fireEvent.click(screen.getByRole("button", { name: "Runner Devices" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Inspect installed harnesses" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Edit executable paths" }),
  );
  for (const harness of ["codex", "claude-code", "opencode", "grok-build"])
    change(`${harness} executable path`, join(directory, `missing-${harness}`));
  fireEvent.click(
    screen.getByRole("button", { name: "Run optional diagnostics" }),
  );
  fireEvent.click(
    await screen.findByRole(
      "button",
      { name: "Finish inspection" },
      { timeout: 15000 },
    ),
  );
  await waitFor(() => {
    for (const harness of ["codex", "claude-code", "opencode", "grok-build"]) {
      const article = screen
        .getByRole("heading", { name: harness })
        .closest("article")!;
      expect(
        within(article).getByText(
          copy(
            harness === "claude-code"
              ? "claude-subscription.excluded.missing"
              : "machine-settings.excluded.missing",
          ),
        ),
      ).toBeTruthy();
    }
  });
  const observedMachines = await createClient(
    ResourceService,
    transport,
  ).listResources({ filter: { kind: EntityKind.MACHINE } });
  expect(document(observedMachines.resources[0]).installations).toEqual(
    expect.arrayContaining(
      ["codex", "claude-code", "opencode", "grok-build"].map((harness) =>
        expect.objectContaining({ harness, state: "missing" }),
      ),
    ),
  );
  // An account-less Agent is valid configuration but cannot infer readiness or
  // launch a harness. The real Worker has only explicit missing executables.
  const configurations = createClient(ConfigurationService, transport);
  const save = async (kind: EntityKind, value: Record<string, unknown>) =>
    (
      await configurations.saveConfiguration({
        kind,
        mutation: { requestId: newRequestId() },
        schemaVersion: 1,
        documentJson: encode(value),
      })
    ).resource!;
  const providerConfig = await save(EntityKind.PROVIDER, {
    name: "Schedule fixture provider",
    endpoint: providerOrigin,
    protocol: "openai-chat",
    authentication: "keyless",
    discovery: false,
  });
  const disconnectedAccount = await save(EntityKind.ACCOUNT, {
    alias: "Disconnected schedule account",
    type: "api",
    provider_id: providerConfig.id,
    enabled: true,
    health: "disconnected",
  });
  const accountlessAgent = (
    await createClient(ConfigurationService, transport).saveConfiguration({
      kind: EntityKind.AGENT,
      mutation: { requestId: newRequestId() },
      schemaVersion: 4,
      documentJson: encode({
        name: "Accountless schedule agent",
        harness: "codex",
        routes: [
          {
            model: {
              provider_id: providerConfig.id,
              native_id: "fixture-model",
              input_modalities: ["text"],
              metadata_source: "unknown",
            },
            accounts: [{ id: disconnectedAccount.id, weight: 1 }],
          },
        ],
        templates: [],
        options: { permission: "default" },
      }),
    })
  ).resource!;
  cleanup();
  const scheduleClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false, gcTime: 0 },
    },
  });
  const readLocalWorker = async () => {
    const credential = JSON.parse(
      await readFile(join(workerRoot, "device.json"), "utf8"),
    );
    return {
      machineId: credential.machine_id as string,
      token: credential.token as string,
    };
  };
  let createdSessionId = "";
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={scheduleClient}>
        <MutationIntents>
          <NewSession
            active
            ownsActivation
            activation={1}
            back={() => {}}
            openSettings={() => {}}
            open={(id) => {
              createdSessionId = id;
            }}
            created={() => {}}
            readLocalWorker={readLocalWorker}
          />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  const newSession = within(
    window.document.querySelector(".new-session-page")!,
  );
  const changeNewSession = (name: string, value: string) =>
    fireEvent.change(newSession.getByLabelText(name), { target: { value } });
  await choose(
    newSession.getByRole("listbox", { name: "Project" }),
    "Owned project",
  );
  fireEvent.click(newSession.getByRole("button", { name: "Options" }));
  fireEvent.click(newSession.getByRole("radio", { name: "Local" }));
  await waitFor(() =>
    expect(
      (newSession.getByLabelText("Runs on") as HTMLSelectElement).disabled,
    ).toBe(true),
  );
  await choose(
    newSession.getByRole("combobox", { name: "Agent Worker" }),
    "Accountless schedule agent",
  );
  changeNewSession("First message", "Local proof fixture without inference");
  fireEvent.click(newSession.getByText("Optional estimated-cost budget"));
  fireEvent.click(
    newSession.getByRole("checkbox", { name: "Enable estimated-cost budget" }),
  );
  changeNewSession("Budget currency", "USD");
  changeNewSession("Estimated-cost threshold", "0.000000000000001");
  fireEvent.click(newSession.getByRole("button", { name: "Create session" }));
  await waitFor(async () => {
    expect(createdSessionId).not.toBe("");
    const sessions = await createClient(
      ResourceService,
      transport,
    ).listResources({ filter: { kind: EntityKind.SESSION } });
    const created = sessions.resources.find(
      (row) => row.id === createdSessionId,
    );
    expect(created && document(created)).toMatchObject({
      workspace: "local",
      name: "New session",
      estimated_cost_budget: {
        currency: "USD",
        threshold: "0.000000000000001",
      },
    });
  });
  cleanup();
  const resources = createClient(ResourceService, transport);
  await waitFor(async () => {
    const session = (
      await resources.getResource({
        kind: EntityKind.SESSION,
        id: createdSessionId,
      })
    ).resource!;
    expect(document(session)).toMatchObject({
      dispatch: "blocked",
      problem: {
        code: "unavailable",
        message:
          "The first execution is waiting for its workspace, Runner Device or account.",
      },
    });
  });
  const budgetSession = (
    await resources.getResource({
      kind: EntityKind.SESSION,
      id: createdSessionId,
    })
  ).resource!;
  expect(document(budgetSession).estimated_cost_budget).toEqual({
    currency: "USD",
    threshold: "0.000000000000001",
  });
  const budgetClient = createClient(SessionService, transport);
  expect(
    (await budgetClient.getSessionBudget({ sessionId: budgetSession.id })).view,
  ).toMatchObject({
    state: BudgetState.ALLOW_INCOMPLETE,
    selectedCurrency: { knownAmount: "" },
  });
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={scheduleClient}>
        <MutationIntents>
          <SessionBudget
            resource={budgetSession}
            changed={() => {}}
            blocked={() => {}}
          />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  await screen.findByText(/Known lifetime subtotal: Unavailable/);
  const editCurrentBudget = async () => {
    await waitFor(async () => {
      // The owned Worker may advance the session while this integration flow is
      // running. Refresh active queries on every poll so an old cache snapshot
      // cannot satisfy the edit gate after a single invalidation.
      await scheduleClient.refetchQueries({ type: "active" });
      const current = (
        await budgetClient.getSessionBudget({ sessionId: budgetSession.id })
      ).view!;
      const observed = scheduleClient
        .getQueryCache()
        .getAll()
        .map((query) => {
          const view = (
            query.state.data as GetSessionBudgetResponse | undefined
          )?.view;
          return {
            id: view?.session?.id,
            revision: view?.session?.revision.toString(),
          };
        });
      expect(observed).toContainEqual({
        id: budgetSession.id,
        revision: current.session!.revision.toString(),
      });
      expect(
        screen
          .getByRole("button", { name: "Edit session budget" })
          .matches(":disabled"),
      ).toBe(false);
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Edit session budget" }),
    );
  };
  const saveBudgetAndRecoverRevision = async (
    resultText: string | RegExp,
    retainedDraft: string | undefined,
  ) => {
    const maxRevisionRecoveries = 3;
    for (let attempt = 0; attempt <= maxRevisionRecoveries; attempt++) {
      fireEvent.click(
        screen.getByRole("button", { name: "Save session budget" }),
      );
      const outcome = await waitFor(async () => {
        if (screen.queryByText(resultText)) return "saved";
        await scheduleClient.refetchQueries({ type: "active" });
        if (
          screen.queryByRole("button", {
            name: "Use latest revision with this draft",
          })
        ) {
          return "stale";
        }
        throw new Error("Budget save or current session revision is pending");
      });
      if (outcome === "saved") return;
      if (retainedDraft !== undefined) {
        expect(
          (
            screen.getByLabelText(
              "Estimated-cost threshold",
            ) as HTMLInputElement
          ).value,
        ).toBe(retainedDraft);
      } else {
        expect(
          (
            screen.getByRole("checkbox", {
              name: "Enable estimated-cost budget",
            }) as HTMLInputElement
          ).checked,
        ).toBe(false);
      }
      if (attempt === maxRevisionRecoveries) {
        throw new Error("Budget session revision kept changing after retries");
      }
      fireEvent.click(
        screen.getByRole("button", {
          name: "Use latest revision with this draft",
        }),
      );
      await waitFor(() =>
        expect(
          (
            screen.getByRole("button", {
              name: "Save session budget",
            }) as HTMLButtonElement
          ).disabled,
        ).toBe(false),
      );
    }
  };
  await editCurrentBudget();
  change("Estimated-cost threshold", "12.345678901234567");
  await saveBudgetAndRecoverRevision(
    /Threshold: USD 12.345678901234567/,
    "12.345678901234567",
  );
  expect(
    (await budgetClient.getSessionBudget({ sessionId: budgetSession.id })).view
      ?.budget?.threshold,
  ).toBe("12.345678901234567");
  await editCurrentBudget();
  const enableBudget = screen.getByRole("checkbox", {
    name: "Enable estimated-cost budget",
  }) as HTMLInputElement;
  fireEvent.click(enableBudget);
  expect(enableBudget.checked).toBe(false);
  const saveBudget = screen.getByRole("button", {
    name: "Save session budget",
  }) as HTMLButtonElement;
  if (saveBudget.disabled) {
    expect(screen.getByRole("alert").textContent).toContain(
      "The session changed. Your budget draft is retained.",
    );
    const latestRevision = screen.getByRole("button", {
      name: "Use latest revision with this draft",
    });
    fireEvent.click(latestRevision);
    await waitFor(() => expect(saveBudget.disabled).toBe(false));
    expect(enableBudget.checked).toBe(false);
  }
  await saveBudgetAndRecoverRevision("Not set", undefined);
  expect(
    (await budgetClient.getSessionBudget({ sessionId: budgetSession.id })).view
      ?.state,
  ).toBe(BudgetState.DISABLED);
  cleanup();
  let generalSessionId = "";
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={scheduleClient}>
        <MutationIntents>
          <NewSession
            kind={NewSessionKind.GeneralChat}
            active
            ownsActivation
            activation={1}
            back={() => {}}
            openSettings={() => {}}
            open={(id) => {
              generalSessionId = id;
            }}
            created={() => {}}
          />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  expect(screen.queryByLabelText("Project")).toBeNull();
  await choose(
    screen.getByRole("combobox", { name: "Agent Worker" }),
    "Accountless schedule agent",
  );
  await choose(
    screen.getByRole("combobox", { name: "Runs on" }),
    "Owned Git Worker",
  );
  change("First message", "General Chat acceptance fixture without inference");
  fireEvent.click(screen.getByRole("button", { name: "Start general chat" }));
  await waitFor(() => expect(generalSessionId).not.toBe(""));
  const generalSession = (
    await createClient(ResourceService, transport).getResource({
      kind: EntityKind.SESSION,
      id: generalSessionId,
    })
  ).resource!;
  expect(generalSession.projectId).toBe("");
  expect(document(generalSession)).toMatchObject({
    workspace: "general-chat",
    name_mode: "automatic",
    source: "MANUAL",
    agent_id: accountlessAgent.id,
  });
  expect(document(generalSession).local_origin).toBeUndefined();
  expect(document(generalSession).project_id).toBeUndefined();
  cleanup();
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={scheduleClient}>
        <MutationIntents>
          <Schedules active open={() => {}} readLocalWorker={readLocalWorker} />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "New schedule" }));
  change("Schedule name", "Owned schedule");
  change("Scheduled prompt", "Private schedule fixture prompt");
  await choose(
    screen.getByRole("combobox", { name: "Project" }),
    "Owned project",
  );
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  await choose(
    screen.getByRole("combobox", { name: "Agent Worker" }),
    "Accountless schedule agent",
  );
  await choose(
    screen.getByRole("combobox", { name: "Runner Device" }),
    "Owned Git Worker",
  );
  fireEvent.click(screen.getByRole("radio", { name: "Local computer" }));
  await waitFor(() =>
    expect(
      (screen.getByLabelText("Runner Device") as HTMLSelectElement).disabled,
    ).toBe(true),
  );
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Enable future scheduled runs" }),
  );
  change("Frequency", "custom");
  change("Cron expression", "0 0 1 1 *");
  change("IANA timezone", "Asia/Seoul");
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  fireEvent.click(screen.getByRole("button", { name: "Create schedule" }));
  // The schedule controls are disabled while a mutation is in flight, and a click
  // on a disabled button is silently dropped. Click only once a control is enabled.
  const clickEnabled = async (name: string) => {
    const button = await screen.findByRole("button", { name });
    await waitFor(() =>
      expect((button as HTMLButtonElement).disabled).toBe(false),
    );
    fireEvent.click(button);
  };
  // A schedule mutation carries the revision the detail view last read. Reread
  // the active schedule queries so the next mutation is not stale.
  const settleScheduleReads = () =>
    act(async () => {
      await scheduleClient.refetchQueries({ type: "active" });
    });
  await clickEnabled("Resume future runs");
  await clickEnabled("Pause future runs");
  await screen.findByRole("button", { name: "Resume future runs" });
  await settleScheduleReads();
  await clickEnabled("Run now");
  await clickEnabled("Confirm Run now");
  await screen.findByText("Run now accepted");
  // Run now appends the occurrence and advances the schedule revision on the
  // server, so the detail view must reread it before Delete can be accepted.
  await settleScheduleReads();
  const schedules = await createClient(
    ScheduleService,
    transport,
  ).listSchedules({});
  const scheduleId = schedules.schedules[0].id;
  expect(document(schedules.schedules[0])).toMatchObject({
    definition: { workspace: "local" },
    local_origin: { machine_id: (await readLocalWorker()).machineId },
  });
  expect(
    (
      await createClient(ScheduleService, transport).listScheduleOccurrences({
        scheduleId,
      })
    ).occurrences,
  ).toHaveLength(1);
  await settleScheduleReads();
  await clickEnabled("Delete schedule");
  await clickEnabled("Confirm schedule deletion");
  // A real durable Go mutation can outlast Testing Library's one-second default
  // under concurrent CI shards. Wait for its receipt, never resend the deletion.
  await screen.findByText(
    "Schedule configuration deleted. Retained occurrences and sessions remain.",
    {},
    { timeout: 15000 },
  );
  expect(
    (
      await createClient(ScheduleService, transport).listScheduleOccurrences({
        scheduleId,
      })
    ).occurrences,
  ).toHaveLength(1);
  cleanup();
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <Settings />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Projects" }));
  fireEvent.click(
    await screen.findByRole("button", {
      name: `Delete Owned project`,
    }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Confirm configuration deletion" }),
  );
  await waitFor(() =>
    expect(
      screen.queryByRole("heading", { name: "Delete Owned project?" }),
    ).toBeNull(),
  );
  expect(
    (
      await createClient(ResourceService, transport).listResources({
        filter: { kind: EntityKind.PROJECT },
      })
    ).resources,
  ).toHaveLength(0);
}, 60000);
