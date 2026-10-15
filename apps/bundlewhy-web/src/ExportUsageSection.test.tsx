import { fromJson } from "@bufbuild/protobuf";
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ExportUsageSection } from "./ExportUsageSection";
import {
  ExportUsagePathEnd,
  ExportUsageSchema,
  ExportUsageState,
  ExportUsageTruncation,
} from "./gen/bundlewhy/v1/bundlewhy_pb";
import { exportEdge, fixtureExportUsage } from "./test-fixtures";

function renderUsage(usage = fixtureExportUsage()) {
  return render(<ExportUsageSection usage={usage} />);
}

function pathItems(index = 1) {
  const list = screen.getByRole("list", {
    name: new RegExp(`^Export usage path ${index},`),
  });
  return within(list).getAllByRole("listitem");
}

describe("export usage section", () => {
  it("lists a chain from the outermost consumer to the diagnostic module", () => {
    renderUsage();
    const items = pathItems();
    expect(items).toHaveLength(2);
    expect(items[0].textContent).toContain("src/main.ts");
    expect(items[0].textContent).toContain("src/lib.ts");
    expect(items[0].textContent).toContain("export foo");
    expect(items[0].textContent).toContain("Dependency ID 27");
    expect(items[0].textContent).toContain(
      "Location in the consumer module 6:13-16",
    );
    expect(items[1].textContent).toContain("src/shared.ts");
    expect(items[1].textContent).toContain("export bar");
    expect(items[1].textContent).toContain("Dependency ID 37");
    const [first, second] = items.map((item) => item.textContent ?? "");
    expect(first.indexOf("src/main.ts")).toBeLessThan(
      first.indexOf("src/lib.ts"),
    );
    expect(second.indexOf("src/lib.ts")).toBeLessThan(
      second.indexOf("src/shared.ts"),
    );
  });

  it("separates engine observations from the derived chain", () => {
    renderUsage();
    expect(screen.getAllByText("Observed by Rspack")).toHaveLength(2);
    expect(screen.getAllByText("Derived by bundlewhy")).toHaveLength(1);
    const legend = document.querySelector(".export-usage-legend")!.textContent!;
    expect(legend).toContain("Rspack reported");
    expect(legend).toContain("derived by bundlewhy");
    expect(legend).toContain(
      "does not show runtime execution, removable code or byte savings",
    );
    // No wording that proves a call or a saving.
    const text = document.body.textContent!;
    expect(text).not.toMatch(/\bcalls?\b/i);
    expect(text).not.toMatch(/\bsaves?\b|\bunused\b/i);
  });

  it("labels module scope without claiming a function call", () => {
    renderUsage();
    const first = pathItems()[0].textContent!;
    expect(first).toContain("module or namespace scope (no named export)");
    expect(first).not.toMatch(/export undefined|export $/);
  });

  it("joins nested export paths and keeps null names distinct", () => {
    renderUsage(
      fixtureExportUsage({
        paths: [
          {
            edges: [
              exportEdge(
                { module: "src/a.ts", exportPath: ["ns", "inner"] },
                { module: "src/b.ts" },
              ),
            ],
            end: ExportUsagePathEnd.ROOT,
          },
        ],
      }),
    );
    const text = pathItems()[0].textContent!;
    expect(text).toContain("export ns.inner");
    expect(text).toContain("module or namespace scope (no named export)");
    expect(text).not.toContain("Dependency ID");
    expect(text).not.toContain("Location in the consumer module");
  });

  it("renders each branch as its own chain", () => {
    renderUsage(
      fixtureExportUsage({
        paths: [
          {
            edges: [
              exportEdge(
                { module: "src/left.ts", exportPath: ["a"] },
                { module: "src/shared.ts", exportPath: ["x"] },
              ),
            ],
            end: ExportUsagePathEnd.ROOT,
          },
          {
            edges: [
              exportEdge(
                { module: "src/right.ts", exportPath: ["b"] },
                { module: "src/shared.ts", exportPath: ["x"] },
              ),
            ],
            end: ExportUsagePathEnd.ROOT,
          },
        ],
      }),
    );
    expect(pathItems(1)[0].textContent).toContain("src/left.ts");
    expect(pathItems(2)[0].textContent).toContain("src/right.ts");
  });

  it("reads the incomplete chain end that the Rust analysis writes", () => {
    const usage = fromJson(ExportUsageSchema, {
      state: "EXPORT_USAGE_STATE_AVAILABLE",
      note: "1 engine edge(s) could not be used and are not shown. Such edges involve modules that bundlewhy could not identify or export references that bundlewhy could not read.",
      paths: [
        {
          edges: [
            {
              consumer: {
                module: "src/a.ts",
                exportPath: ["a"],
                scope: "EXPORT_SCOPE_NAMED",
              },
              provider: {
                module: "src/b.ts",
                exportPath: ["b"],
                scope: "EXPORT_SCOPE_NAMED",
              },
            },
          ],
          end: "EXPORT_USAGE_PATH_END_INCOMPLETE",
        },
      ],
      truncation: "EXPORT_USAGE_TRUNCATION_NONE",
    });
    expect(usage.paths[0].end).toBe(ExportUsagePathEnd.INCOMPLETE);
    renderUsage(usage);
    expect(document.querySelector(".export-path-end")!.textContent).toMatch(
      /Chain start not confirmed/,
    );
    expect(document.querySelector(".export-path-end")!.textContent).not.toMatch(
      /Chain start: Rspack reported no earlier edge/,
    );
    expect(screen.getByText(/1 engine edge\(s\)/)).toBeTruthy();
  });

  it("explains how each chain ends", () => {
    const ends: [ExportUsagePathEnd, RegExp][] = [
      [ExportUsagePathEnd.ROOT, /Chain start: Rspack reported no earlier edge/],
      [
        ExportUsagePathEnd.CYCLE,
        /returns to an export that is already in this chain/,
      ],
      [ExportUsagePathEnd.TRUNCATED, /stopped at a limit before the start/],
      [
        ExportUsagePathEnd.INCOMPLETE,
        /Chain start not confirmed.*could not use.*export reference it can read.*may be an earlier edge.*may continue beyond/,
      ],
      [ExportUsagePathEnd.UNSPECIFIED, /end of this chain was not recorded/],
    ];
    for (const [end, pattern] of ends) {
      const { unmount } = renderUsage(
        fixtureExportUsage({
          paths: [
            {
              edges: [
                exportEdge({ module: "src/a.ts" }, { module: "src/b.ts" }),
              ],
              end,
            },
          ],
        }),
      );
      expect(document.querySelector(".export-path-end")!.textContent).toMatch(
        pattern,
      );
      unmount();
    }
  });

  it("shows a visible marker for every truncation reason and none otherwise", () => {
    const reasons: [ExportUsageTruncation, RegExp | null][] = [
      [ExportUsageTruncation.PATH_COUNT, /More chains exist than are shown/],
      [ExportUsageTruncation.DEPTH, /longer than the display limit/],
      [ExportUsageTruncation.EXPANSION_BUDGET, /work budget was exhausted/],
      [ExportUsageTruncation.NONE, null],
      [ExportUsageTruncation.UNSPECIFIED, null],
    ];
    for (const [truncation, pattern] of reasons) {
      const { unmount } = renderUsage(fixtureExportUsage({ truncation }));
      const marker = screen.queryByRole("note");
      if (pattern) {
        expect(marker?.textContent).toMatch(pattern);
        expect(marker?.textContent).toContain("Truncated:");
      } else {
        expect(marker).toBeNull();
      }
      unmount();
    }
  });

  it("states that export usage is unavailable and keeps the reason", () => {
    renderUsage(
      fixtureExportUsage({
        state: ExportUsageState.UNAVAILABLE,
        note: "This Rspack version does not provide experiments.RsdoctorPlugin.",
        paths: [],
        truncation: ExportUsageTruncation.UNSPECIFIED,
      }),
    );
    const message = document.querySelector(".export-usage-unavailable")!;
    expect(message.textContent).toContain(
      "Export usage is unavailable for this diagnostic.",
    );
    expect(message.textContent).toContain("experiments.RsdoctorPlugin");
    expect(message.textContent).toContain(
      "Other evidence and the module dependency paths are not affected.",
    );
    expect(screen.queryByRole("list")).toBeNull();
    expect(screen.queryByText("Observed by Rspack")).toBeNull();
  });

  it("falls back to a generic reason when none was recorded", () => {
    renderUsage(
      fixtureExportUsage({
        state: ExportUsageState.UNSPECIFIED,
        paths: [],
      }),
    );
    expect(
      document.querySelector(".export-usage-unavailable")!.textContent,
    ).toContain("The reason was not recorded.");
  });

  it("discloses engine edges that could not be used", () => {
    renderUsage(
      fixtureExportUsage({
        note: "3 engine edge(s) could not be used and are not shown. Such edges involve modules that bundlewhy could not identify or export references that bundlewhy could not read.",
      }),
    );
    expect(
      screen.getByText(
        /3 engine edge\(s\) could not be used.*export references/,
      ),
    ).toBeTruthy();
    expect(pathItems()).toHaveLength(2);
  });

  it("does not fail on an edge without a module reference", () => {
    renderUsage(
      fixtureExportUsage({
        paths: [
          {
            edges: [
              {
                provider: exportEdge(
                  { module: "src/a.ts" },
                  { module: "src/b.ts" },
                ).provider,
              },
            ],
            end: ExportUsagePathEnd.ROOT,
          },
        ],
      }),
    );
    expect(pathItems()[0].textContent).toContain("unknown module");
    expect(pathItems()[0].textContent).toContain("src/b.ts");
  });
});
