import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi, type Mock } from "vitest";
import {
  DestinationWindowNotice,
  DestinationWindowProvider,
  useDestinationDispatch,
  useDestinationWindowController,
} from "./destination-window";
import { i18n, SupportedLanguage } from "./localization";
import type { ProductRoute } from "./product-route";
import { Surface } from "./surface";

const native = vi.hoisted(() => ({
  invoke: vi.fn(),
  isTauri: vi.fn(() => true),
}));
vi.mock("@tauri-apps/api/core", () => native);

const session = "018f0000-0000-7000-8000-000000000001";
const route: ProductRoute = { surface: Surface.Sessions, session };

function setPlatform(platform: string) {
  vi.spyOn(navigator, "platform", "get").mockReturnValue(platform);
}

let current: Mock<() => void>;
let parentClick: Mock<() => void>;

function Control({ typed = true }: { typed?: boolean }) {
  const dispatch = useDestinationDispatch();
  return (
    <div onClick={() => parentClick()}>
      <button
        type="button"
        onClick={(event) => dispatch(event, typed ? route : undefined, () => current())}
      >
        Destination
      </button>
    </div>
  );
}

function Harness({ typed }: { typed?: boolean }) {
  const controller = useDestinationWindowController();
  return (
    <DestinationWindowProvider value={controller}>
      <DestinationWindowNotice />
      <Control typed={typed} />
    </DestinationWindowProvider>
  );
}

beforeEach(async () => {
  await i18n.changeLanguage(SupportedLanguage.English);
  native.invoke.mockReset();
  native.invoke.mockResolvedValue(undefined);
  native.isTauri.mockReturnValue(true);
  current = vi.fn();
  parentClick = vi.fn();
  setPlatform("MacIntel");
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const button = () => screen.getByRole("button", { name: "Destination" });
const modified = { metaKey: true, detail: 1 };

it("opens one destination window for a primary modified click and does not navigate the current window", async () => {
  render(<Harness />);
  const event = new MouseEvent("click", {
    bubbles: true,
    cancelable: true,
    metaKey: true,
    detail: 1,
  });
  await act(async () => {
    button().dispatchEvent(event);
  });
  expect(native.invoke).toHaveBeenCalledTimes(1);
  expect(native.invoke).toHaveBeenCalledWith("open_destination_window", {
    destination: { screen: Surface.Sessions, session },
  });
  expect(current).not.toHaveBeenCalled();
  // The default action and bubbling are suppressed for a handled click.
  expect(event.defaultPrevented).toBe(true);
  expect(parentClick).not.toHaveBeenCalled();
  expect(screen.queryByRole("alert")).toBeNull();
});

it("accepts Shift with the platform primary modifier and Control on other platforms", async () => {
  render(<Harness />);
  await act(async () => {
    fireEvent.click(button(), { ...modified, shiftKey: true });
  });
  expect(native.invoke).toHaveBeenCalledTimes(1);
  setPlatform("Win32");
  await act(async () => {
    fireEvent.click(button(), { ctrlKey: true, detail: 1 });
  });
  expect(native.invoke).toHaveBeenCalledTimes(2);
  expect(current).not.toHaveBeenCalled();
});

it("keeps current-window navigation for plain clicks, keyboard activation and excluded modifiers", async () => {
  render(<Harness />);
  const cases: [string, Parameters<typeof fireEvent.click>[1]][] = [
    ["plain", { detail: 1 }],
    ["keyboard", { detail: 0 }],
    ["keyboard with modifier", { metaKey: true, detail: 0 }],
    ["wrong modifier", { ctrlKey: true, detail: 1 }],
    ["alt", { metaKey: true, altKey: true, detail: 1 }],
    ["ctrl+meta", { metaKey: true, ctrlKey: true, detail: 1 }],
    ["middle button", { metaKey: true, button: 1, detail: 1 }],
  ];
  for (const [, init] of cases) {
    const event = new MouseEvent("click", {
      bubbles: true,
      cancelable: true,
      ...init,
    });
    await act(async () => {
      button().dispatchEvent(event);
    });
    expect(event.defaultPrevented).toBe(false);
  }
  expect(current).toHaveBeenCalledTimes(cases.length);
  expect(native.invoke).not.toHaveBeenCalled();
  expect(parentClick).toHaveBeenCalledTimes(cases.length);
});

it("never selects a new window for a right-button event", async () => {
  render(<Harness />);
  // React does not deliver a secondary-button click to onClick, and a
  // contextmenu gesture is not a click; neither may open a window.
  await act(async () => {
    fireEvent.click(button(), { metaKey: true, button: 2, detail: 1 });
    fireEvent.contextMenu(button(), { metaKey: true, button: 2, detail: 1 });
    fireEvent.mouseUp(button(), { metaKey: true, button: 2, detail: 1 });
  });
  expect(native.invoke).not.toHaveBeenCalled();
});

it("makes no native attempt in a web preview or mobile host", async () => {
  native.isTauri.mockReturnValue(false);
  render(<Harness />);
  await act(async () => {
    fireEvent.click(button(), modified);
  });
  expect(native.invoke).not.toHaveBeenCalled();
  expect(current).toHaveBeenCalledTimes(1);
});

it("keeps the current navigation for a control without a typed destination", async () => {
  render(<Harness typed={false} />);
  await act(async () => {
    fireEvent.click(button(), modified);
  });
  expect(native.invoke).not.toHaveBeenCalled();
  expect(current).toHaveBeenCalledTimes(1);
});

it("outside a Shell the dispatcher always runs the current navigation", async () => {
  render(<Control />);
  fireEvent.click(button(), modified);
  expect(native.invoke).not.toHaveBeenCalled();
  expect(current).toHaveBeenCalledTimes(1);
});

it("admits one pending operation: a repeated activation is suppressed, not queued, not navigated", async () => {
  let finish!: () => void;
  native.invoke.mockReturnValueOnce(
    new Promise<void>((resolve) => {
      finish = resolve;
    }),
  );
  render(<Harness />);
  await act(async () => {
    fireEvent.click(button(), modified);
  });
  const repeated = new MouseEvent("click", {
    bubbles: true,
    cancelable: true,
    ...modified,
  });
  await act(async () => {
    button().dispatchEvent(repeated);
  });
  expect(repeated.defaultPrevented).toBe(true);
  expect(native.invoke).toHaveBeenCalledTimes(1);
  expect(current).not.toHaveBeenCalled();
  await act(async () => finish());
  // The slot is released after settlement: the next activation opens a window.
  await act(async () => {
    fireEvent.click(button(), modified);
  });
  expect(native.invoke).toHaveBeenCalledTimes(2);
});

it("shows a localized operation-scoped failure without fallback, retry or logging the destination", async () => {
  const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
  const info = vi.spyOn(console, "info").mockImplementation(() => {});
  native.invoke.mockRejectedValueOnce("permission-denied");
  render(<Harness />);
  await act(async () => {
    fireEvent.click(button(), modified);
  });
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toBe(
    "The destination could not be opened in a new window. This window is unchanged.",
  );
  expect(current).not.toHaveBeenCalled();
  expect(native.invoke).toHaveBeenCalledTimes(1);
  expect(warn).toHaveBeenCalledWith("delidev.destination_window", {
    phase: "failed",
    code: "permission-denied",
  });
  const logged = JSON.stringify([...warn.mock.calls, ...info.mock.calls]);
  expect(logged).not.toContain(session);
  expect(logged).not.toContain("sessions");
  // Dismissing clears the notice; a later activation is a new operation.
  fireEvent.click(screen.getByRole("button", { name: "Dismiss new window error" }));
  expect(screen.queryByRole("alert")).toBeNull();
  await act(async () => {
    fireEvent.click(button(), modified);
  });
  expect(native.invoke).toHaveBeenCalledTimes(2);
  expect(screen.queryByRole("alert")).toBeNull();
});

it("clears a previous failure when the next operation starts", async () => {
  native.invoke.mockRejectedValueOnce("busy");
  render(<Harness />);
  await act(async () => {
    fireEvent.click(button(), modified);
  });
  await screen.findByRole("alert");
  await act(async () => {
    fireEvent.click(button(), modified);
  });
  await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
});

it("reports unknown native rejections with the same localized failure", async () => {
  native.invoke.mockRejectedValueOnce(new Error("https://example.invalid/"));
  vi.spyOn(console, "warn").mockImplementation(() => {});
  render(<Harness />);
  await act(async () => {
    fireEvent.click(button(), modified);
  });
  expect(await screen.findByRole("alert")).toBeTruthy();
});

it("localizes the failure in Korean", async () => {
  await act(() => i18n.changeLanguage(SupportedLanguage.Korean));
  native.invoke.mockRejectedValueOnce("stopped");
  vi.spyOn(console, "warn").mockImplementation(() => {});
  render(<Harness />);
  await act(async () => {
    fireEvent.click(button(), modified);
  });
  expect((await screen.findByRole("alert")).textContent).toContain("새 창");
});

it("does not update state after the Shell unmounts while an operation is pending", async () => {
  let fail!: (reason: string) => void;
  native.invoke.mockReturnValueOnce(
    new Promise<void>((_, reject) => {
      fail = reject;
    }),
  );
  const error = vi.spyOn(console, "error").mockImplementation(() => {});
  vi.spyOn(console, "warn").mockImplementation(() => {});
  const view = render(<Harness />);
  await act(async () => {
    fireEvent.click(button(), modified);
  });
  view.unmount();
  await act(async () => fail("busy"));
  expect(error).not.toHaveBeenCalled();
});
