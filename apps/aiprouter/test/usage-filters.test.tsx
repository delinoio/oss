// SPDX-License-Identifier: Apache-2.0
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, it, expect, rs } from "@rstest/core";
import { createRouterTransport, type ConnectRouter } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { QueryClient } from "@tanstack/react-query";
import {
  GetSessionResponseSchema,
  OrganizationSchema,
  Role,
  UsageService,
} from "@aiprouter/client";
import { ApiProvider } from "../src/api";
import { UsagePage, defaultUsageRange } from "../src/usage";
import type { ReactNode } from "react";

const organization = create(OrganizationSchema, {
  id: "one",
  name: "Studio",
  role: Role.MEMBER,
});
const session = create(GetSessionResponseSchema, {
  userId: "person",
  displayName: "Alex",
});
const props = { organization, session };
const RANGE_ERROR =
  "Choose a valid date range. The end date must be on or after the start date.";

function setWidth(width: number) {
  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    writable: true,
    value: width,
  });
}
const INLINE = 1280;
const NARROW = 800;

function mount(ui: ReactNode, routes: (r: ConnectRouter) => void) {
  const cache = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <ApiProvider
      queryClient={cache}
      rpcTransport={createRouterTransport(routes)}
    >
      {ui}
    </ApiProvider>,
  );
}
function mocks() {
  const listUsage = rs.fn((_request: unknown) => ({ records: [] }));
  const getUsageSummary = rs.fn((_request: unknown) => ({}));
  const listDailyUsage = rs.fn((_request: unknown) => ({ days: [] }));
  return {
    listUsage,
    getUsageSummary,
    listDailyUsage,
    all: [listUsage, getUsageSummary, listDailyUsage],
    routes: (r: ConnectRouter) =>
      r.service(UsageService, { listUsage, getUsageSummary, listDailyUsage }),
  };
}
const lastInput = (mock: { mock: { lastCall?: unknown[] } }) =>
  mock.mock.lastCall?.[0] as Record<string, any>;

afterEach(() => {
  rs.useRealTimers();
  setWidth(1024);
});

describe("default range and serialization", () => {
  it("computes the UTC default range from today minus six days", () => {
    expect(defaultUsageRange(Date.parse("2026-10-27T23:59:59Z"))).toEqual({
      start: "2026-10-21",
      end: "2026-10-27",
    });
    expect(defaultUsageRange(Date.parse("2026-10-28T00:00:00Z"))).toEqual({
      start: "2026-10-22",
      end: "2026-10-28",
    });
  });

  it("sends the frozen default range and identity to all three RPCs", async () => {
    rs.useFakeTimers({ toFake: ["Date"] });
    rs.setSystemTime(new Date("2026-10-27T15:00:00Z"));
    setWidth(INLINE);
    const m = mocks();
    mount(<UsagePage {...props} />, m.routes);
    await waitFor(() => {
      for (const mock of m.all) expect(mock).toHaveBeenCalled();
    });
    expect(
      (screen.getByLabelText("Start date (UTC)") as HTMLInputElement).value,
    ).toBe("2026-10-21");
    expect(
      (screen.getByLabelText("End date (UTC)") as HTMLInputElement).value,
    ).toBe("2026-10-27");
    const start = timestampFromDate(new Date("2026-10-21T00:00:00Z"));
    const end = timestampFromDate(new Date("2026-10-27T23:59:59.999Z"));
    for (const mock of m.all) {
      const input = lastInput(mock);
      expect(input.organizationId).toBe("one");
      expect(input.userId).toBe("person");
      expect(input.timeRange.start.seconds).toBe(start.seconds);
      expect(input.timeRange.end.seconds).toBe(end.seconds);
      expect(input.timeRange.end.nanos).toBe(end.nanos);
      expect(input.modelId).toBeUndefined();
      expect(input.providerId).toBeUndefined();
      expect(input.outcome).toBeUndefined();
    }
  });

  it("trims model and provider, omits blanks and sends Unknown outcome 0", async () => {
    setWidth(INLINE);
    const user = userEvent.setup();
    const m = mocks();
    mount(<UsagePage {...props} />, m.routes);
    await user.type(screen.getByLabelText("Model ID"), "  gpt-x  ");
    await user.type(screen.getByLabelText("Provider ID"), "   ");
    await user.selectOptions(screen.getByLabelText("Outcome"), "0");
    await waitFor(() => {
      for (const mock of m.all)
        expect(lastInput(mock)).toMatchObject({
          modelId: "gpt-x",
          outcome: 0,
        });
    });
    for (const mock of m.all)
      expect(lastInput(mock).providerId).toBeUndefined();
    await user.selectOptions(screen.getByLabelText("Outcome"), "-1");
    await waitFor(() => {
      for (const mock of m.all) expect(lastInput(mock).outcome).toBeUndefined();
    });
  });
});

describe("single filter form per breakpoint", () => {
  it("renders one inline form with Clear filters last at 1024px and wider", () => {
    setWidth(1024);
    mount(<UsagePage {...props} />, mocks().routes);
    const group = screen.getByRole("group", { name: "Usage filters" });
    expect(screen.getAllByLabelText("Model ID")).toHaveLength(1);
    expect(screen.getAllByLabelText("Outcome")).toHaveLength(1);
    expect(screen.queryByRole("button", { name: /^Filters/ })).toBeNull();
    expect(group.lastElementChild?.textContent).toBe("Clear filters");
    expect(
      within(group)
        .getAllByText(/\(UTC\)|ID|Outcome/)
        .map((el) => el.textContent),
    ).toEqual([
      "Start date (UTC)",
      "End date (UTC)",
      "Model ID",
      "Provider ID",
      "Outcome",
    ]);
  });

  it("renders no filter controls below 1024px until the dialog opens, then exactly one set", async () => {
    setWidth(NARROW);
    const user = userEvent.setup();
    mount(<UsagePage {...props} />, mocks().routes);
    expect(screen.queryByLabelText("Model ID")).toBeNull();
    expect(screen.queryByRole("group", { name: "Usage filters" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Filters (0)" }));
    const dialog = screen.getByRole("dialog", { name: "Usage filters" });
    expect(screen.getAllByLabelText("Model ID")).toHaveLength(1);
    expect(screen.getAllByLabelText("Start date (UTC)")).toHaveLength(1);
    expect(
      within(dialog).getByText(
        "Changes update your usage immediately. Dates are in UTC.",
      ),
    ).toBeTruthy();
    expect(
      within(dialog)
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual(["×", "Clear filters", "Done"]);
  });

  it("switches form location on resize and drops an open dialog", async () => {
    setWidth(NARROW);
    const user = userEvent.setup();
    mount(<UsagePage {...props} />, mocks().routes);
    await user.click(screen.getByRole("button", { name: "Filters (0)" }));
    act(() => {
      setWidth(INLINE);
      window.dispatchEvent(new Event("resize"));
    });
    await waitFor(() =>
      expect(screen.getByRole("group", { name: "Usage filters" })).toBeTruthy(),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.getAllByLabelText("Model ID")).toHaveLength(1);
    act(() => {
      setWidth(NARROW);
      window.dispatchEvent(new Event("resize"));
    });
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Filters (0)" })).toBeTruthy(),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

describe("Filters (N) dialog behavior", () => {
  it("counts raw model, provider and outcome but never dates", async () => {
    setWidth(NARROW);
    const user = userEvent.setup();
    mount(<UsagePage {...props} />, mocks().routes);
    await user.click(screen.getByRole("button", { name: "Filters (0)" }));
    fireEvent.change(screen.getByLabelText("Start date (UTC)"), {
      target: { value: "2026-01-01" },
    });
    await user.type(screen.getByLabelText("Model ID"), " ");
    await user.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.getByRole("button", { name: "Filters (1)" })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Filters (1)" }));
    await user.type(screen.getByLabelText("Provider ID"), "openai");
    await user.selectOptions(screen.getByLabelText("Outcome"), "0");
    await user.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.getByRole("button", { name: "Filters (3)" })).toBeTruthy();
  });

  it("focuses the heading, applies changes live and retains values on Done, X and Escape", async () => {
    setWidth(NARROW);
    const user = userEvent.setup();
    const m = mocks();
    mount(<UsagePage {...props} />, m.routes);
    const trigger = () => screen.getByRole("button", { name: /^Filters/ });
    await user.click(trigger());
    expect(document.activeElement).toBe(
      screen.getByRole("heading", { name: "Usage filters" }),
    );
    await user.type(screen.getByLabelText("Model ID"), "gpt-x");
    await waitFor(() => expect(lastInput(m.listUsage).modelId).toBe("gpt-x"));
    await user.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger());
    await user.click(trigger());
    expect((screen.getByLabelText("Model ID") as HTMLInputElement).value).toBe(
      "gpt-x",
    );
    await user.click(screen.getByRole("button", { name: "Close dialog" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    await user.click(trigger());
    fireEvent(
      screen.getByRole("dialog"),
      new Event("cancel", { cancelable: true }),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    await user.click(trigger());
    expect((screen.getByLabelText("Model ID") as HTMLInputElement).value).toBe(
      "gpt-x",
    );
    expect(lastInput(m.getUsageSummary).modelId).toBe("gpt-x");
  });

  it("Clear filters in the dialog restores defaults and keeps the dialog open", async () => {
    rs.useFakeTimers({ toFake: ["Date"] });
    rs.setSystemTime(new Date("2026-10-27T15:00:00Z"));
    setWidth(NARROW);
    const user = userEvent.setup();
    mount(<UsagePage {...props} />, mocks().routes);
    await user.click(screen.getByRole("button", { name: "Filters (0)" }));
    await user.type(screen.getByLabelText("Model ID"), "m");
    fireEvent.change(screen.getByLabelText("End date (UTC)"), {
      target: { value: "2026-10-01" },
    });
    await user.click(screen.getByRole("button", { name: "Clear filters" }));
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect((screen.getByLabelText("Model ID") as HTMLInputElement).value).toBe(
      "",
    );
    expect(
      (screen.getByLabelText("End date (UTC)") as HTMLInputElement).value,
    ).toBe("2026-10-27");
    expect(
      (screen.getByLabelText("Start date (UTC)") as HTMLInputElement).value,
    ).toBe("2026-10-21");
  });
});

describe("invalid date range", () => {
  it("associates errors with fields, disables every query and retains entered values", async () => {
    setWidth(INLINE);
    const user = userEvent.setup();
    const m = mocks();
    mount(<UsagePage {...props} />, m.routes);
    await waitFor(() => {
      for (const mock of m.all) expect(mock).toHaveBeenCalled();
    });
    const before = m.all.map((mock) => mock.mock.calls.length);
    fireEvent.change(screen.getByLabelText("Start date (UTC)"), {
      target: { value: "2099-01-01" },
    });
    const end = screen.getByLabelText("End date (UTC)");
    expect(end.getAttribute("aria-invalid")).toBe("true");
    const described = end.getAttribute("aria-describedby")!;
    expect(document.getElementById(described)?.textContent).toBe(
      "The end date must be on or after the start date.",
    );
    expect(
      screen.getByLabelText("Start date (UTC)").getAttribute("aria-invalid"),
    ).toBeNull();
    expect(screen.getByRole("alert").textContent).toBe(RANGE_ERROR);
    expect(
      (screen.getByLabelText("Start date (UTC)") as HTMLInputElement).value,
    ).toBe("2099-01-01");
    expect(screen.getAllByText("Unknown").length).toBeGreaterThan(0);
    expect(screen.queryByText("Requests over time")).toBeNull();
    expect(screen.queryByText("No matching requests")).toBeNull();
    for (const name of ["First page", "Next page"])
      expect(
        (screen.getByRole("button", { name }) as HTMLButtonElement).disabled,
      ).toBe(true);
    // Blank dates are invalid too, with the error on the blank field.
    fireEvent.change(screen.getByLabelText("Start date (UTC)"), {
      target: { value: "" },
    });
    expect(
      screen.getByLabelText("Start date (UTC)").getAttribute("aria-invalid"),
    ).toBe("true");
    fireEvent.change(screen.getByLabelText("End date (UTC)"), {
      target: { value: "" },
    });
    expect(
      screen.getByLabelText("End date (UTC)").getAttribute("aria-invalid"),
    ).toBe("true");
    await act(async () => {
      await new Promise((r) => setTimeout(r, 30));
    });
    expect(m.all.map((mock) => mock.mock.calls.length)).toEqual(before);
    // Recover with a valid range.
    fireEvent.change(screen.getByLabelText("Start date (UTC)"), {
      target: { value: "2026-10-01" },
    });
    fireEvent.change(screen.getByLabelText("End date (UTC)"), {
      target: { value: "2026-10-02" },
    });
    await waitFor(() => {
      for (const mock of m.all) expect(lastInput(mock).timeRange).toBeTruthy();
    });
    expect(screen.queryByRole("alert")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Clear filters" }));
  });

  it("reopens the dialog with the first invalid date focused and keeps the error after Done", async () => {
    setWidth(NARROW);
    const user = userEvent.setup();
    mount(<UsagePage {...props} />, mocks().routes);
    await user.click(screen.getByRole("button", { name: "Filters (0)" }));
    fireEvent.change(screen.getByLabelText("Start date (UTC)"), {
      target: { value: "" },
    });
    expect(
      screen.getByLabelText("Start date (UTC)").getAttribute("aria-invalid"),
    ).toBe("true");
    // Fixing the field while open must not re-run the focus effect.
    const heading = screen.getByRole("heading", { name: "Usage filters" });
    expect(document.activeElement).toBe(heading);
    await user.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.getByRole("alert").textContent).toBe(RANGE_ERROR);
    await user.click(screen.getByRole("button", { name: "Filters (0)" }));
    expect(document.activeElement).toBe(
      screen.getByLabelText("Start date (UTC)"),
    );
  });
});

describe("independent cursors", () => {
  function cursorMocks() {
    const listUsage = rs.fn((request: { page?: { pageToken?: string } }) => ({
      records: [],
      page: request.page?.pageToken ? {} : { nextPageToken: "R2" },
    }));
    const listDailyUsage = rs.fn(
      (request: { page?: { pageToken?: string } }) => ({
        days: [],
        page: request.page?.pageToken ? {} : { nextPageToken: "D2" },
      }),
    );
    const getUsageSummary = rs.fn((_request: unknown) => ({}));
    return {
      listUsage,
      listDailyUsage,
      getUsageSummary,
      routes: (r: ConnectRouter) =>
        r.service(UsageService, {
          listUsage,
          getUsageSummary,
          listDailyUsage,
        }),
    };
  }
  const nextEnabled = async (group: HTMLElement) =>
    waitFor(() => {
      const button = within(group).getByRole("button", { name: "Next page" });
      expect((button as HTMLButtonElement).disabled).toBe(false);
      return button;
    });

  it("pages records and daily usage independently and resets both on any filter edit, including whitespace", async () => {
    setWidth(INLINE);
    const user = userEvent.setup();
    const m = cursorMocks();
    mount(<UsagePage {...props} />, m.routes);
    const daily = await screen.findByRole("group", {
      name: "Daily usage pages",
    });
    const recordsGroup = screen
      .getAllByRole("button", { name: "Next page" })
      .map((b) => b.parentElement!)
      .find((el) => el !== daily)!;
    await user.click(await nextEnabled(recordsGroup));
    await waitFor(() =>
      expect(lastInput(m.listUsage).page.pageToken).toBe("R2"),
    );
    expect(lastInput(m.listDailyUsage).page.pageToken).toBe("");
    await user.click(await nextEnabled(daily));
    await waitFor(() =>
      expect(lastInput(m.listDailyUsage).page.pageToken).toBe("D2"),
    );
    expect(lastInput(m.listUsage).page.pageToken).toBe("R2");
    expect(lastInput(m.getUsageSummary).page).toBeUndefined();
    // Whitespace edit: trimmed inputs are unchanged but both cursors reset.
    await user.type(screen.getByLabelText("Model ID"), " ");
    await waitFor(() => {
      expect(lastInput(m.listUsage).page.pageToken).toBe("");
      expect(lastInput(m.listDailyUsage).page.pageToken).toBe("");
    });
    expect(lastInput(m.listUsage).modelId).toBeUndefined();
  });
});
