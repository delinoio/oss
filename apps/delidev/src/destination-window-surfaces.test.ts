// SPDX-License-Identifier: Apache-2.0
import { readdirSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// Failure presentation per caller context. Every file with a destination
// control is listed with the context its controls render in. A modal dialog
// (and the compact drawer) makes the page inert, so these contexts must
// register a failure surface that presents the notice inside themselves; a new
// caller fails this test until it gets a row here and a presentation test.
enum Context {
  Page = "page",
  Modal = "modal",
  Drawer = "drawer",
  Popover = "popover",
}

const callers: Record<string, Context> = {
  "App.tsx": Context.Page,
  "inbox.tsx": Context.Page,
  "pull-requests.tsx": Context.Page,
  "schedules.tsx": Context.Page,
  "settings.tsx": Context.Page,
  "usage.tsx": Context.Page,
  "session-fork.tsx": Context.Modal,
  "sidebar.tsx": Context.Drawer,
  "sidebar-more.tsx": Context.Popover,
};

const source = (file: string) => readFileSync(`src/${file}`, "utf8");

describe("destination window failure surfaces", () => {
  it("lists every file with a destination control", () => {
    const found = readdirSync("src")
      .filter(
        (file) =>
          /\.tsx?$/.test(file) &&
          !/\.test\./.test(file) &&
          file !== "destination-window.tsx",
      )
      .filter((file) =>
        /useDestinationDispatch\(\)|destinationWindow\.claim\(/.test(
          source(file),
        ),
      )
      .sort();
    expect(found).toEqual(Object.keys(callers).sort());
  });

  it("registers a surface that renders the notice for every non-page context", () => {
    const registrations: Record<
      Exclude<Context, Context.Page>,
      [file: string, hook: string]
    > = {
      [Context.Modal]: ["ui.tsx", "useDestinationSurface(visible)"],
      [Context.Drawer]: [
        "sidebar.tsx",
        "useDestinationSurface(compact && drawerOpen)",
      ],
      [Context.Popover]: ["sidebar-more.tsx", "useDestinationSurface(open)"],
    };
    for (const [context, [file, hook]] of Object.entries(registrations)) {
      const text = source(file);
      expect(text, `${context}: ${file}`).toContain(hook);
      expect(text, `${context}: ${file}`).toContain("destinationSurface.props");
      expect(text, `${context}: ${file}`).toContain(
        "destinationSurface.notice",
      );
    }
    // The modal caller renders its controls through the shared Modal.
    expect(source("session-fork.tsx")).toContain("<Modal");
  });

  it("keeps page contexts free of dialogs that would hide the page notice", () => {
    for (const [file, context] of Object.entries(callers))
      if (context === Context.Page && file !== "App.tsx") {
        const text = source(file);
        expect(text, file).not.toContain("<Modal");
        expect(text, file).not.toContain("<DialogSurface");
      }
  });

  it("renders the page notice once in the Shell main content", () => {
    const app = source("App.tsx");
    expect(app.match(/<DestinationWindowNotice \/>/g)).toHaveLength(1);
    expect(app).toMatch(/<main[^>]*>\s*<DestinationWindowNotice \/>/s);
  });
});
