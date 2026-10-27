// SPDX-License-Identifier: Apache-2.0
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, rs } from "@rstest/core";
import { create } from "@bufbuild/protobuf";
import {
  OrganizationSchema,
  GetSessionResponseSchema,
  Role,
} from "@aiprouter/client";
import { ConnectPage, isValidConnectProfile } from "../src/overview";
import { pageHref } from "../src/routes";

const session = create(GetSessionResponseSchema, {
  userId: "person",
  displayName: "Alex",
});
const studio = create(OrganizationSchema, {
  id: "one",
  name: "Studio",
  role: Role.MEMBER,
});
const other = create(OrganizationSchema, {
  id: "two",
  name: "Other Team",
  role: Role.MEMBER,
});
const origin = window.location.origin;
const connectCommand = (profile: string) =>
  `aiprouter connect --server '${origin}' --profile '${profile}'`;
const configureCommand = (profile: string) =>
  `aiprouter configure --profile '${profile}' --client CLIENT_ID`;
const disconnectCommand = (profile: string) =>
  `aiprouter disconnect --profile '${profile}'`;
const codeBlocks = () =>
  Array.from(document.querySelectorAll("pre code")).map((c) => c.textContent);
function mount(organization = studio) {
  return render(<ConnectPage organization={organization} session={session} />);
}
function stubClipboard(writeText: (text: string) => Promise<void>) {
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
}

describe("ConnectPage content", () => {
  it("renders the default profile, context strip and exact commands", () => {
    mount();
    expect(
      screen.getAllByRole("heading", { level: 1 }).map((h) => h.textContent),
    ).toEqual(["Connect a client"]);
    expect(
      screen.getByText("Connect to Studio with your own routing."),
    ).toBeTruthy();
    const context = document.querySelector(".connect-context") as HTMLElement;
    expect(within(context).getByText("Server")).toBeTruthy();
    expect(within(context).getByText(origin)).toBeTruthy();
    expect(within(context).getByText("Organization")).toBeTruthy();
    expect(within(context).getByText("Studio")).toBeTruthy();
    expect(codeBlocks()).toEqual([
      connectCommand("default"),
      "aiprouter clients",
      configureCommand("default"),
      disconnectCommand("default"),
    ]);
    expect(
      (screen.getByLabelText("Profile name") as HTMLInputElement).value,
    ).toBe("default");
    expect(
      screen.getByText(
        "Start with a lowercase letter. Use lowercase letters, digits, and hyphens; at most 48 characters.",
      ),
    ).toBeTruthy();
    expect(screen.queryByText(/valid profile name/)).toBeNull();
    expect(
      screen.getByLabelText("Profile name").getAttribute("aria-invalid"),
    ).toBeNull();
  });

  it("keeps the DOM order header, context, step 1, step 2 and recovery with static step numbers", () => {
    mount();
    const order = Array.from(
      document.querySelectorAll(
        ".page-heading, .connect-context, .connect-step-one, .connect-step-two, .connect-recovery",
      ),
    ).map((node) => node.className.split(" ").pop());
    expect(order).toEqual([
      "page-heading",
      "connect-context",
      "connect-step-one",
      "connect-step-two",
      "connect-recovery",
    ]);
    expect(
      screen.getByRole("heading", {
        level: 2,
        name: /Start a secure connection/,
      }).textContent,
    ).toBe("1Start a secure connection");
    expect(
      screen.getByRole("heading", {
        level: 2,
        name: /Choose and configure your client/,
      }).textContent,
    ).toBe("2Choose and configure your client");
    expect(screen.queryByText(/connected/i)).toBeNull();
  });

  it("keeps instructional and warning copy visible without installer or client picker", () => {
    mount();
    expect(
      screen.getByText(
        "Run this command with the installed AIPRouter CLI. Choose Studio when approving the device in your browser. Confirm the browser code matches your CLI.",
      ),
    ).toBeTruthy();
    expect(
      screen.getByText(
        "The CLI stores the resulting key in your OS credential store.",
      ),
    ).toBeTruthy();
    expect(screen.getByText("Review before applying")).toBeTruthy();
    expect(
      screen.getByText(
        "The CLI previews settings changes and asks before applying them. A separate confirmation is required if a client stores the remote key in its settings.",
      ),
    ).toBeTruthy();
    expect(
      screen.getByText(
        "Clients that need a loopback relay show an explicit local-access warning. Review it before installing the profile service.",
      ),
    ).toBeTruthy();
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(
      screen.queryByText(/install(er)? command|brew|download/i),
    ).toBeNull();
  });

  it("shows three always-visible recovery sections as conditional advice", () => {
    mount();
    const recovery = document.querySelector(".connect-recovery") as HTMLElement;
    expect(
      within(recovery)
        .getAllByRole("heading", { level: 3 })
        .map((h) => h.textContent),
    ).toEqual([
      "Credential store unavailable",
      "Server revocation incomplete",
      "Restoration conflicts",
    ]);
    expect(
      within(recovery).getByText(/There is no plaintext credential fallback\./),
    ).toBeTruthy();
    expect(
      within(recovery).getByText(
        "If server revocation could not finish, open API keys and revoke the outstanding CLI key.",
      ),
    ).toBeTruthy();
    expect(
      within(recovery).getByText(
        /Server key revocation and local settings restoration are separate outcomes\./,
      ),
    ).toBeTruthy();
    expect(recovery.querySelector(".danger, [role='alert']")).toBeNull();
    expect(
      within(recovery)
        .getByRole("link", { name: "Open API keys" })
        .getAttribute("href"),
    ).toBe(pageHref("keys", "one"));
  });

  it("has exactly one button and it is the primary copy button", () => {
    mount();
    const buttons = screen.getAllByRole("button");
    expect(buttons.map((b) => b.textContent)).toEqual(["Copy connect command"]);
    expect(buttons[0].className).toContain("primary");
  });
});

describe("ConnectPage profile validation", () => {
  it("focuses the profile input on mount and never refocuses on keystrokes or scope refresh", async () => {
    const user = userEvent.setup();
    const view = mount();
    const input = screen.getByLabelText("Profile name");
    expect(document.activeElement).toBe(input);
    const link = screen.getByRole("link", { name: "Open API keys" });
    link.focus();
    view.rerender(<ConnectPage organization={other} session={session} />);
    expect(document.activeElement).toBe(link);
    await user.click(input);
    await user.type(input, "x");
    link.focus();
    expect(document.activeElement).toBe(link);
    expect((input as HTMLInputElement).value).toBe("defaultx");
  });

  it.each([["a"], ["team-2"], ["a".repeat(48)], ["a-"], ["a--b"]])(
    "accepts %s and updates every dependent command",
    async (profile) => {
      const user = userEvent.setup();
      mount();
      const input = screen.getByLabelText("Profile name");
      await user.clear(input);
      await user.type(input, profile);
      expect(input.getAttribute("aria-invalid")).toBeNull();
      expect(codeBlocks()).toEqual([
        connectCommand(profile),
        "aiprouter clients",
        configureCommand(profile),
        disconnectCommand(profile),
      ]);
      expect(
        screen.getByRole("button", { name: "Copy connect command" }),
      ).toBeTruthy();
      expect(screen.queryByText(/Commands below use default/)).toBeNull();
    },
  );

  it.each([
    ["empty", ""],
    ["49 characters", "a".repeat(49)],
    ["uppercase", "Team"],
    ["leading digit", "1team"],
    ["leading hyphen", "-team"],
    ["underscore", "team_2"],
    ["space", "team 2"],
    ["leading space", " team"],
    ["trailing space", "team "],
    ["apostrophe", "team'x"],
    ["shell fragment", "a;rm -rf /"],
    ["command substitution", "a$(id)"],
  ])("rejects %s without interpolating it", async (_name, profile) => {
    const user = userEvent.setup();
    mount();
    const input = screen.getByLabelText("Profile name") as HTMLInputElement;
    await user.clear(input);
    // Set the value directly so control characters reach the controlled input unchanged.
    if (profile) {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!;
      const { fireEvent } = await import("@testing-library/react");
      setter.call(input, profile);
      fireEvent.input(input);
    }
    expect(input.getAttribute("aria-invalid")).toBe("true");
    const error = screen.getByText(
      "Enter a valid profile name before copying the command.",
    );
    const described = (input.getAttribute("aria-describedby") ?? "").split(" ");
    expect(described).toContain(error.id);
    expect(
      screen.getByText(
        /Commands below use default until the profile name is valid\./,
      ),
    ).toBeTruthy();
    expect(
      described.some((id) =>
        document
          .getElementById(id)
          ?.textContent?.includes("Commands below use default"),
      ),
    ).toBe(true);
    expect(codeBlocks()).toEqual([
      connectCommand("default"),
      "aiprouter clients",
      configureCommand("default"),
      disconnectCommand("default"),
    ]);
    expect(
      screen.queryByRole("button", { name: "Copy connect command" }),
    ).toBeNull();
    expect(document.body.textContent).not.toContain(profile.trim() || "\u0000");
  });

  it("restores copy when the profile becomes valid again", async () => {
    const user = userEvent.setup();
    mount();
    const input = screen.getByLabelText("Profile name");
    await user.clear(input);
    await user.type(input, "Bad");
    expect(
      screen.queryByRole("button", { name: "Copy connect command" }),
    ).toBeNull();
    await user.clear(input);
    await user.type(input, "good");
    expect(input.getAttribute("aria-invalid")).toBeNull();
    expect(
      screen.getByRole("button", { name: "Copy connect command" }),
    ).toBeTruthy();
  });
});

describe("ConnectPage profile rule", () => {
  it("never trims or normalizes values, including line breaks", () => {
    // A text input strips line breaks itself, so the rule is asserted directly here.
    for (const value of ["team\nx", "team\n", "\nteam", "team\r", " team"])
      expect(isValidConnectProfile(value)).toBe(false);
    expect(isValidConnectProfile("a".repeat(48))).toBe(true);
    expect(isValidConnectProfile("a".repeat(49))).toBe(false);
  });
});

describe("ConnectPage clipboard", () => {
  it("copies the exact displayed command and announces success without moving focus", async () => {
    const user = userEvent.setup();
    const writeText = rs.fn((_text: string) => Promise.resolve());
    mount();
    stubClipboard(writeText);
    const input = screen.getByLabelText("Profile name");
    await user.clear(input);
    await user.type(input, "team-2");
    const button = screen.getByRole("button", { name: "Copy connect command" });
    expect(screen.getByRole("status").textContent).toBe("");
    await user.click(button);
    expect(writeText).toHaveBeenCalledTimes(1);
    expect(writeText).toHaveBeenCalledWith(connectCommand("team-2"));
    expect(await screen.findByText("Copied.")).toBeTruthy();
    expect(screen.getByRole("status").getAttribute("aria-live")).toBe("polite");
    expect(document.activeElement).toBe(button);
  });

  it("reports failure and keeps the command selectable when the clipboard rejects", async () => {
    const user = userEvent.setup();
    const writeText = rs.fn((_text: string) =>
      Promise.reject(new Error("denied")),
    );
    mount();
    stubClipboard(writeText);
    await user.click(
      screen.getByRole("button", { name: "Copy connect command" }),
    );
    expect(
      await screen.findByText(
        "Copy failed. Select and copy the value manually.",
      ),
    ).toBeTruthy();
    expect(screen.queryByText("Copied.")).toBeNull();
    expect(codeBlocks()[0]).toBe(connectCommand("default"));
  });
});

describe("ConnectPage organization scope", () => {
  it("updates context, guidance and the API keys link together when the organization changes", async () => {
    const user = userEvent.setup();
    const view = mount();
    const input = screen.getByLabelText("Profile name");
    await user.clear(input);
    await user.type(input, "team-2");
    view.rerender(<ConnectPage organization={other} session={session} />);
    expect(
      screen.getByText("Connect to Other Team with your own routing."),
    ).toBeTruthy();
    expect(screen.queryByText(/Studio/)).toBeNull();
    expect(
      screen.getByText(/Choose Other Team when approving the device/),
    ).toBeTruthy();
    expect(
      screen.getByRole("link", { name: "Open API keys" }).getAttribute("href"),
    ).toBe(pageHref("keys", "two"));
    // The profile is component-local state and is neither reset nor persisted by a scope change.
    expect(
      (screen.getByLabelText("Profile name") as HTMLInputElement).value,
    ).toBe("team-2");
    expect(localStorage.length).toBe(0);
  });
});
