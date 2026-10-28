// SPDX-License-Identifier: Apache-2.0
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, rs } from "@rstest/core";
import { createRouterTransport, ConnectError, Code } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { QueryClient } from "@tanstack/react-query";
import {
  RoutingService,
  ProviderService,
  PoolService,
  RouteSchema,
  RoutingStrategy,
  ConnectionState,
  OrganizationSchema,
  GetSessionResponseSchema,
  type Route,
} from "@aiprouter/client";
import { ApiProvider } from "../src/api";
import { RoutingPage } from "../src/routing";

const ASCII_512 = "a".repeat(512);
const ASCII_513 = "a".repeat(513);
const KOREAN_512 = "한".repeat(170) + "aa";
const KOREAN_513 = "한".repeat(170) + "aaa";

function routeWithTargets(
  strategy: RoutingStrategy,
  count: number,
  name = "wide-route",
): Route {
  return create(RouteSchema, {
    id: name,
    modelId: name,
    enabled: true,
    strategy,
    targets: Array.from({ length: count }, (_, i) => ({
      target: { case: "connectionId" as const, value: "target" },
      modelId: `model-${i}`,
      priority: i,
      weight: 1 + (i % 7),
    })),
  });
}
function mount(
  routes: Route[],
  mutate: (request: unknown) => object = () => ({}),
) {
  const transport = createRouterTransport((r) => {
    r.service(ProviderService, {
      listProviderConnections: () => ({
        connections: [
          {
            id: "target",
            name: "Inference account",
            state: ConnectionState.READY,
          },
          { id: "other", name: "Other account", state: ConnectionState.READY },
        ],
      }),
    });
    r.service(PoolService, {
      listPools: () => ({ pools: [{ id: "pool", name: "Backup pool" }] }),
    });
    r.service(RoutingService, {
      listRoutes: () => ({ routes }),
      createRoute: mutate,
      updateRoute: mutate,
    });
  });
  render(
    <ApiProvider
      rpcTransport={transport}
      queryClient={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <RoutingPage
        organization={create(OrganizationSchema, { id: "org", name: "Studio" })}
        session={create(GetSessionResponseSchema, { userId: "person" })}
      />
    </ApiProvider>,
  );
}
async function openEdit(name: string) {
  const heading = await screen.findByRole("heading", { name });
  await userEvent.click(
    within(heading.closest("section")!).getByRole("button", {
      name: "Edit route",
    }),
  );
  return screen.findByRole("dialog", { name: "Edit routing" });
}
async function openCreate() {
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Create route" }));
  return screen.findByRole("dialog", { name: "Create route" });
}
function setValue(element: HTMLElement, value: string) {
  fireEvent.change(element, { target: { value } });
}
function saveButton() {
  return screen.getByRole("button", {
    name: "Save routing",
  }) as HTMLButtonElement;
}
async function fillCreateTarget(alias: string, modelId: string) {
  const user = userEvent.setup();
  setValue(screen.getByLabelText("Model alias"), alias);
  await user.click(screen.getByRole("button", { name: "Add target" }));
  await user.selectOptions(
    screen.getByLabelText("Connection or pool"),
    "connectionId:target",
  );
  setValue(screen.getByLabelText("Provider model ID"), modelId);
}

describe("Route editor details and ordering", () => {
  it("keeps the specified reading order with retention after targets", async () => {
    mount([routeWithTargets(RoutingStrategy.FALLBACK, 1)]);
    const dialog = await openEdit("wide-route");
    const text = (name: string) => within(dialog).getByText(name);
    const before = (a: HTMLElement, b: HTMLElement) =>
      expect(
        a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
    before(text("Route details"), text("Ordered targets"));
    before(text("Ordered targets"), text("Reasoning retention"));
    before(
      text("Reasoning retention"),
      text(
        "Saving replaces the complete route. Changing away from JEV removes its decision configuration.",
      ),
    );
    expect(
      within(dialog).getByText(
        "Retry behavior follows each provider's policy.",
      ),
    ).toBeTruthy();
    expect(document.activeElement).toBe(
      within(dialog).getByLabelText("Model alias"),
    );
  });

  it("renders ordinal target cards and announces move, add and remove politely", async () => {
    const user = userEvent.setup();
    mount([routeWithTargets(RoutingStrategy.FALLBACK, 2)]);
    const dialog = await openEdit("wide-route");
    expect(within(dialog).getByText("1 · Primary")).toBeTruthy();
    expect(within(dialog).getByText("2 · Fallback 1")).toBeTruthy();
    const live = dialog.querySelector('[aria-live="polite"]')!;
    expect(live.getAttribute("role")).toBe("status");
    await user.click(
      within(dialog).getAllByRole("button", { name: "Move up" })[1]!,
    );
    expect(live.textContent).toBe("Moved target 2 up to position 1.");
    expect(
      (
        within(dialog).getAllByLabelText(
          "Provider model ID",
        ) as HTMLInputElement[]
      ).map((input) => input.value),
    ).toEqual(["model-1", "model-0"]);
    await user.click(
      within(dialog).getByRole("button", { name: "Add target" }),
    );
    expect(live.textContent).toBe("Added target 3.");
    const added = within(dialog).getAllByLabelText("Connection or pool")[2]!;
    expect(document.activeElement).toBe(added);
    expect(
      (within(dialog).getAllByLabelText("Weight")[2] as HTMLInputElement).value,
    ).toBe("1");
    await user.click(
      within(dialog).getAllByRole("button", { name: "Remove target" })[2]!,
    );
    expect(live.textContent).toBe("Removed target 3. 2 targets remaining.");
    expect(within(dialog).getAllByLabelText("Provider model ID")).toHaveLength(
      2,
    );
  });

  it("saves trimmed alias, mixed oneofs and priority equal to position after reorder", async () => {
    const user = userEvent.setup();
    const mutate = rs.fn((_request: unknown) => ({}));
    mount([], mutate);
    await openCreate();
    await user.click(screen.getByRole("button", { name: "Add target" }));
    await user.click(screen.getByRole("button", { name: "Add target" }));
    setValue(screen.getByLabelText("Model alias"), "  chat-primary  ");
    const selects = screen.getAllByLabelText("Connection or pool");
    await user.selectOptions(selects[0]!, "connectionId:target");
    await user.selectOptions(selects[1]!, "poolId:pool");
    const models = screen.getAllByLabelText("Provider model ID");
    setValue(models[0]!, "first");
    setValue(models[1]!, " second ");
    await user.click(screen.getAllByRole("button", { name: "Move up" })[1]!);
    await user.click(saveButton());
    expect(mutate).toHaveBeenCalledOnce();
    expect(mutate.mock.calls[0]![0]).toMatchObject({
      modelId: "chat-primary",
      strategy: RoutingStrategy.FALLBACK,
      enabled: true,
      targets: [
        {
          target: { case: "poolId", value: "pool" },
          modelId: " second ",
          priority: 0,
          weight: 1,
        },
        {
          target: { case: "connectionId", value: "target" },
          modelId: "first",
          priority: 1,
          weight: 1,
        },
      ],
    });
    expect(
      (mutate.mock.calls[0]![0] as { targets: object[] }).targets[0],
    ).not.toHaveProperty("key");
  });
});

describe("Route editor UTF-8 byte validation", () => {
  it.each([
    ["ASCII 512", ASCII_512, true],
    ["ASCII 513", ASCII_513, false],
    ["multibyte 512", KOREAN_512, true],
    ["multibyte 513", KOREAN_513, false],
  ] as const)("alias %s bytes", async (_label, alias, valid) => {
    const mutate = rs.fn((_request: unknown) => ({}));
    mount([], mutate);
    await openCreate();
    await fillCreateTarget(alias, "model");
    const input = screen.getByLabelText("Model alias");
    if (valid) {
      expect(saveButton().disabled).toBe(false);
      await userEvent.click(saveButton());
      expect(mutate.mock.calls[0]![0]).toMatchObject({ modelId: alias });
    } else {
      expect(saveButton().disabled).toBe(true);
      expect(input.getAttribute("aria-invalid")).toBe("true");
      const error = screen.getAllByText(
        "Model alias must be 512 UTF-8 bytes or fewer.",
      );
      expect(
        error.some((node) =>
          input.getAttribute("aria-describedby")?.includes(node.id),
        ),
      ).toBe(true);
      expect(mutate).not.toHaveBeenCalled();
      expect((input as HTMLInputElement).value).toBe(alias);
    }
  });
  it("measures the alias after trimming", async () => {
    const mutate = rs.fn((_request: unknown) => ({}));
    mount([], mutate);
    await openCreate();
    await fillCreateTarget(`  ${ASCII_512}  `, "model");
    expect(saveButton().disabled).toBe(false);
    await userEvent.click(saveButton());
    expect(mutate.mock.calls[0]![0]).toMatchObject({ modelId: ASCII_512 });
  });
  it.each([
    ["ASCII 512", ASCII_512, true],
    ["ASCII 513", ASCII_513, false],
    ["multibyte 512", KOREAN_512, true],
    ["multibyte 513", KOREAN_513, false],
    ["padded 513", ` ${ASCII_512}`, false],
  ] as const)("provider model ID %s bytes", async (_label, modelId, valid) => {
    const mutate = rs.fn((_request: unknown) => ({}));
    mount([], mutate);
    await openCreate();
    await fillCreateTarget("alias", modelId);
    const input = screen.getByLabelText("Provider model ID");
    if (valid) {
      expect(saveButton().disabled).toBe(false);
      await userEvent.click(saveButton());
      expect(mutate.mock.calls[0]![0]).toMatchObject({
        targets: [{ modelId }],
      });
    } else {
      expect(saveButton().disabled).toBe(true);
      expect(input.getAttribute("aria-invalid")).toBe("true");
      expect(
        screen.getAllByText(
          "Provider model ID must be 512 UTF-8 bytes or fewer.",
        ).length,
      ).toBeGreaterThan(0);
      expect(mutate).not.toHaveBeenCalled();
      expect((input as HTMLInputElement).value).toBe(modelId);
    }
  });
  it("rejects blank model IDs and keeps non-blank IDs unchanged", async () => {
    mount([]);
    await openCreate();
    await fillCreateTarget("alias", "   ");
    expect(saveButton().disabled).toBe(true);
    expect(
      screen.getAllByText("Provider model ID is required.").length,
    ).toBeGreaterThan(0);
  });
});

describe("Route editor target count rules", () => {
  it.each([
    RoutingStrategy.FALLBACK,
    RoutingStrategy.WEIGHTED,
    RoutingStrategy.ROUND_ROBIN,
  ])(
    "allows 100 targets and blocks the 101st for strategy %s",
    async (strategy) => {
      const user = userEvent.setup();
      const mutate = rs.fn((_request: unknown) => ({}));
      mount([routeWithTargets(strategy, 100)], mutate);
      const dialog = await openEdit("wide-route");
      expect(saveButton().disabled).toBe(false);
      await user.click(
        within(dialog).getByRole("button", { name: "Add target" }),
      );
      const rows = within(dialog).getAllByLabelText("Provider model ID");
      expect(rows).toHaveLength(101);
      expect(saveButton().disabled).toBe(true);
      const add = within(dialog).getByRole("button", { name: "Add target" });
      const groupError = within(dialog).getAllByText(
        "This routing strategy needs 1 to 100 targets.",
      );
      expect(
        groupError.some((node) =>
          add.getAttribute("aria-describedby")?.includes(node.id),
        ),
      ).toBe(true);
      expect(within(dialog).getByRole("alert").textContent).toContain(
        "This routing strategy needs 1 to 100 targets.",
      );
      expect(mutate).not.toHaveBeenCalled();
      await user.click(
        within(dialog).getAllByRole("button", { name: "Remove target" })[100]!,
      );
      expect(saveButton().disabled).toBe(false);
      await user.click(saveButton());
      const request = mutate.mock.calls[0]![0] as {
        strategy: RoutingStrategy;
        targets: { modelId: string; priority: number; weight: number }[];
      };
      expect(request.strategy).toBe(strategy);
      expect(request.targets).toHaveLength(100);
      request.targets.forEach((t, i) => {
        expect(t.modelId).toBe(`model-${i}`);
        expect(t.priority).toBe(i);
        expect(t.weight).toBe(1 + (i % 7));
      });
    },
    60000,
  );
  it("applies the 64 target cap only to JEV and exactly one to Direct", async () => {
    const user = userEvent.setup();
    mount([routeWithTargets(RoutingStrategy.FALLBACK, 65)]);
    const dialog = await openEdit("wide-route");
    expect(saveButton().disabled).toBe(false);
    await user.selectOptions(
      within(dialog).getByLabelText("Routing strategy"),
      String(RoutingStrategy.DIRECT),
    );
    expect(saveButton().disabled).toBe(true);
    expect(
      within(dialog).getAllByText("Direct routing needs exactly one target.")
        .length,
    ).toBeGreaterThan(0);
    await user.selectOptions(
      within(dialog).getByLabelText("Routing strategy"),
      String(RoutingStrategy.JEV),
    );
    expect(saveButton().disabled).toBe(true);
    expect(
      within(dialog).getAllByText("JEV routing needs 1 to 64 targets.").length,
    ).toBeGreaterThan(0);
    await user.selectOptions(
      within(dialog).getByLabelText("Routing strategy"),
      String(RoutingStrategy.WEIGHTED),
    );
    expect(saveButton().disabled).toBe(false);
  }, 60000);
  it("blocks zero targets and a direct route with two targets", async () => {
    const user = userEvent.setup();
    mount([routeWithTargets(RoutingStrategy.DIRECT, 2)]);
    const dialog = await openEdit("wide-route");
    expect(saveButton().disabled).toBe(true);
    await user.click(
      within(dialog).getAllByRole("button", { name: "Remove target" })[1]!,
    );
    expect(saveButton().disabled).toBe(false);
    await user.click(
      within(dialog).getByRole("button", { name: "Remove target" }),
    );
    expect(saveButton().disabled).toBe(true);
  });
  it("validates weight bounds with an associated explanation", async () => {
    mount([routeWithTargets(RoutingStrategy.WEIGHTED, 1)]);
    const dialog = await openEdit("wide-route");
    const weight = within(dialog).getByLabelText("Weight");
    for (const [value, valid] of [
      ["0", false],
      ["1001", false],
      ["1", true],
      ["1000", true],
    ] as const) {
      setValue(weight, value);
      expect(saveButton().disabled).toBe(!valid);
      expect(weight.getAttribute("aria-invalid")).toBe(valid ? null : "true");
      expect(weight.getAttribute("aria-describedby")).toBeTruthy();
    }
  });
  it("keeps Unavailable target visible and blocks Save without substituting it", async () => {
    mount([
      create(RouteSchema, {
        id: "missing",
        modelId: "missing",
        enabled: true,
        strategy: RoutingStrategy.FALLBACK,
        targets: [
          {
            target: { case: "connectionId", value: "gone" },
            modelId: "model",
            weight: 1,
          },
        ],
      }),
    ]);
    const dialog = await openEdit("missing");
    expect(within(dialog).getByText("Unavailable target")).toBeTruthy();
    expect(saveButton().disabled).toBe(true);
    expect(within(dialog).getByRole("alert").textContent).toContain(
      "Target 1: This connection or pool is unavailable.",
    );
  });
});

describe("Route editor error summary and server rejection", () => {
  it("explains duplicate alias and links the summary to the field", async () => {
    const user = userEvent.setup();
    mount([routeWithTargets(RoutingStrategy.FALLBACK, 1, "taken")]);
    await openCreate();
    setValue(screen.getByLabelText("Model alias"), " taken ");
    const alias = screen.getByLabelText("Model alias");
    expect(alias.getAttribute("aria-invalid")).toBe("true");
    const summary = screen.getByTestId("error-summary");
    expect(summary.textContent).toContain(
      "This name is already in use. Choose a different name.",
    );
    expect(summary.textContent).toContain("needs 1 to 100 targets");
    await user.click(
      within(summary).getByRole("link", {
        name: "This name is already in use. Choose a different name.",
      }),
    );
    expect(document.activeElement).toBe(alias);
  });
  it("keeps required errors quiet until the form is touched", async () => {
    mount([]);
    await openCreate();
    expect(screen.queryByTestId("error-summary")).toBeNull();
    expect(saveButton().disabled).toBe(true);
    setValue(screen.getByLabelText("Model alias"), "x");
    expect(screen.getByTestId("error-summary").textContent).toContain(
      "needs 1 to 100 targets",
    );
  });
  it("retains the dialog and complete draft after a 101-target or 513-byte rejection, then retries", async () => {
    const user = userEvent.setup();
    let calls = 0;
    const mutate = rs.fn((_request: unknown) => {
      calls += 1;
      if (calls === 1)
        throw new ConnectError("too many targets", Code.InvalidArgument);
      return {};
    });
    mount([], mutate);
    await openCreate();
    await fillCreateTarget("alias", "model-a");
    await user.click(screen.getByRole("button", { name: "Add target" }));
    const selects = screen.getAllByLabelText("Connection or pool");
    await user.selectOptions(selects[1]!, "poolId:pool");
    setValue(screen.getAllByLabelText("Provider model ID")[1]!, "model-b");
    await user.click(saveButton());
    const dialog = await screen.findByRole("dialog", { name: "Create route" });
    expect(
      await within(dialog).findByText(/could not be completed|invalid|Check/i),
    ).toBeTruthy();
    expect(
      (screen.getByLabelText("Model alias") as HTMLInputElement).value,
    ).toBe("alias");
    expect(
      (screen.getAllByLabelText("Provider model ID") as HTMLInputElement[]).map(
        (i) => i.value,
      ),
    ).toEqual(["model-a", "model-b"]);
    expect(
      (
        screen.getAllByLabelText("Connection or pool") as HTMLSelectElement[]
      ).map((s) => s.value),
    ).toEqual(["connectionId:target", "poolId:pool"]);
    expect(within(dialog).getByRole("status").textContent).not.toMatch(
      /saved/i,
    );
    setValue(screen.getAllByLabelText("Provider model ID")[1]!, "model-c");
    await user.click(saveButton());
    await waitFor(() => expect(mutate).toHaveBeenCalledTimes(2));
    expect(mutate.mock.calls[1]![0]).toMatchObject({
      modelId: "alias",
      targets: [{ modelId: "model-a" }, { modelId: "model-c" }],
    });
  });
});
