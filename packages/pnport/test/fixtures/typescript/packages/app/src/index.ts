import { readFileSync } from "node:fs";
import { answer } from "@fixture/core";
import type { Name } from "@fixture/scoped";
import type { Name as Alias } from "alias";
export const result: number = answer;
export const name: Name & Alias = "fixture";
export const read = (path: string): string => readFileSync(path, "utf8");
