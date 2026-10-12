// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { repositoryDetailsGeometry } from "./repository-details-popup";

it("places details eight pixels right without changing the source geometry", () => {
  const row = { left: 80, right: 320, top: 100, bottom: 140 };
  expect(repositoryDetailsGeometry(row, { left: 0, top: 0, width: 1440, height: 900 }, 80)).toEqual({ left: 328, top: 100, width: 320, maxHeight: 884 });
  expect(row).toEqual({ left: 80, right: 320, top: 100, bottom: 140 });
});

it("falls below or above without covering the opener and constrains long details", () => {
  const viewport = { left: 0, top: 0, width: 480, height: 320 };
  expect(repositoryDetailsGeometry({ left: 120, right: 380, top: 20, bottom: 60 }, viewport, 100)).toEqual({ left: 120, top: 68, width: 320, maxHeight: 244 });
  expect(repositoryDetailsGeometry({ left: 120, right: 380, top: 260, bottom: 300 }, viewport, 100)).toEqual({ left: 120, top: 152, width: 320, maxHeight: 244 });
  const long = repositoryDetailsGeometry({ left: 120, right: 380, top: 100, bottom: 140 }, viewport, 2000);
  expect(long.maxHeight).toBe(164);
  expect(long.top + long.maxHeight).toBe(312);
});

it("uses the visual viewport's offsets and clamps narrow effective zoom layouts", () => {
  const viewport = { left: 30, top: 40, width: 280, height: 240 };
  const result = repositoryDetailsGeometry({ left: 20, right: 240, top: 60, bottom: 100 }, viewport, 900);
  expect(result.left).toBe(38);
  expect(result.width).toBe(264);
  expect(result.top).toBe(108);
  expect(result.maxHeight).toBe(164);
});

it("gives an oversized wrapped opener a usable viewport scroll area", () => {
  const result = repositoryDetailsGeometry(
    { left: 120, right: 380, top: -100, bottom: 500 },
    { left: 0, top: 0, width: 480, height: 320 },
    2000,
  );
  expect(result.top).toBe(8);
  expect(result.maxHeight).toBe(304);
});
