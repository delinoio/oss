// SPDX-License-Identifier: Apache-2.0
import { expect, test } from "vitest";
import contrastFixtures from "./appearance-contrast-fixtures.json";
import {
  colorContrastRequirements,
  contrast,
  palettes,
  validColors,
  validTheme,
  type ColorMap,
} from "./appearance-preferences";

type Mode = "light" | "dark";
const modes: Mode[] = ["light", "dark"];
// The text, link and focus rows added for issue 2316. The native validator
// carries the same rows and consumes the same fixtures file.
const addedRows: [keyof ColorMap, keyof ColorMap, number][] = [
  ["link", "surface", 4.5],
  ["success-text", "surface-subtle", 4.5],
  ["conversation-text", "warning-background", 4.5],
  ["focus", "surface", 3],
  ["focus", "surface-selected", 3],
];
const expectedDefaultRatios: Record<Mode, number[]> = {
  light: [5.448, 6.695, 10.604, 4.978, 3.939],
  dark: [8.941, 10.418, 9.644, 9.701, 6.207],
};

test("the shared contrast matrix contains every added row exactly once", () => {
  for (const row of addedRows)
    expect(
      colorContrastRequirements.filter(
        ([fg, bg, minimum]) =>
          fg === row[0] && bg === row[1] && minimum === row[2],
      ),
    ).toHaveLength(1);
  expect(colorContrastRequirements).toHaveLength(16);
});

for (const mode of modes) {
  test(`Default ${mode} passes the added rows with the documented ratios`, () => {
    const colors = palettes.default[mode];
    expect(validColors(colors)).toBe(true);
    addedRows.forEach(([fg, bg, minimum], index) => {
      const ratio = contrast(colors[fg], colors[bg]);
      expect(ratio, `${fg} / ${bg}`).toBeGreaterThanOrEqual(minimum);
      expect(ratio).toBeCloseTo(expectedDefaultRatios[mode][index], 2);
    });
  });

  test(`Default ${mode} rejects a 1:1 foreground for each added row`, () => {
    for (const [fg, bg] of addedRows) {
      const colors = { ...palettes.default[mode], [fg]: palettes.default[mode][bg] };
      expect(contrast(colors[fg], colors[bg]), `${fg} / ${bg}`).toBe(1);
      expect(validColors(colors), `${fg} / ${bg}`).toBe(false);
      const theme = {
        version: 1,
        name: "Fixture",
        ...structuredClone(palettes.default),
        [mode]: colors,
      };
      expect(validTheme(theme), `${fg} / ${bg}`).toBe(false);
    }
  });
}

test("opaque threshold fixtures sit just below and just above each minimum", () => {
  expect(contrastFixtures).toHaveLength(16);
  for (const fixture of contrastFixtures) {
    const token = fixture.token as keyof ColorMap;
    const colors = {
      ...palettes.default[fixture.mode as Mode],
      [token]: fixture.value,
    };
    const margins = addedRows
      .filter(([fg]) => fg === token)
      .map(([, bg, minimum]) => contrast(fixture.value, colors[bg]) - minimum);
    const margin = Math.min(...margins);
    expect(margin >= 0, `${fixture.mode} ${token} ${fixture.value}`).toBe(
      fixture.accepted,
    );
    expect(Math.abs(margin)).toBeLessThan(0.07);
    expect(validColors(colors), `${fixture.mode} ${token}`).toBe(
      fixture.accepted,
    );
  }
});

test("every bundled map satisfies the complete contrast matrix", () => {
  const ids = Object.keys(palettes);
  expect(ids).toHaveLength(5);
  for (const id of ids)
    for (const mode of modes) {
      const colors = palettes[id as keyof typeof palettes][mode];
      for (const [fg, bg, minimum] of colorContrastRequirements)
        expect(
          contrast(colors[fg], colors[bg]),
          `${id} ${mode} ${fg} / ${bg}`,
        ).toBeGreaterThanOrEqual(minimum);
      expect(validColors(colors), `${id} ${mode}`).toBe(true);
    }
});

test("existing closed-key, opaque-hex and contrast checks still reject", () => {
  const colors = palettes.default.light;
  expect(validColors({ ...colors, extra: "#000000" })).toBe(false);
  expect(validColors({ ...colors, link: "#0000FF80" })).toBe(false);
  expect(validColors({ ...colors, link: "blue" })).toBe(false);
  expect(validColors({ ...colors, text: colors.surface })).toBe(false);
  expect(validColors({ ...colors, "control-border": colors.surface })).toBe(
    false,
  );
  expect(validColors({ ...colors, "on-inverse": colors["inverse-surface"] })).toBe(
    false,
  );
});
