import { createElement } from "react";
import type { ElementProps, TextStyle } from "./types.js";

export interface Address { row: number; column: number }
export interface Range { first: Address; last: Address }
export type CachedValue = string | number | boolean;
export type Value = CachedValue | null | { type: "date"; value: string } | { type: "formula"; expression: string; cached?: CachedValue };
export interface CellFormat { style?: TextStyle; numberFormat?: string; wrap?: boolean; border?: boolean }
export enum Comparison { Between = "between", NotBetween = "not_between", Equal = "equal", NotEqual = "not_equal", GreaterThan = "greater_than", LessThan = "less_than", GreaterOrEqual = "greater_or_equal", LessOrEqual = "less_or_equal" }
export enum ThresholdKind { Minimum = "minimum", Maximum = "maximum", Number = "number", Percent = "percent", Percentile = "percentile", Formula = "formula" }
export interface Threshold { kind: ThresholdKind; value?: string }
export enum IconSet { ThreeArrows = "three_arrows", ThreeTrafficLights = "three_traffic_lights", FourArrows = "four_arrows", FiveArrows = "five_arrows" }
export type ConditionalRule =
  | { type: "cell_value"; operator: Comparison; values: string[]; format: CellFormat }
  | { type: "formula"; formula: string; format: CellFormat }
  | { type: "color_scale"; thresholds: Threshold[]; colors: string[] }
  | { type: "data_bar"; minimum: Threshold; maximum: Threshold; color: string; hideValue?: boolean }
  | { type: "icon_set"; icons: IconSet; thresholds: Threshold[]; reverse?: boolean; hideValue?: boolean };
export enum ValidationKind { List = "list", Integer = "integer", Decimal = "decimal", Date = "date", Time = "time", TextLength = "text_length", Custom = "custom" }
export interface ChartProps extends ElementProps {
  kind: "bar" | "line" | "pie"; title?: string; categories: string[];
  series: { name: string; values: number[] }[]; legend?: boolean; labels?: boolean;
  at?: Address; width?: number; height?: number; alt?: string;
}
const component = <P extends ElementProps>(name: string) => (props: P) => createElement(`xlsx:${name}`, props);
export const Workbook = component<ElementProps>("workbook");
export const Worksheet = component<ElementProps & { name: string; freeze?: Address; autofilter?: Range }>("worksheet");
/** Omit address only when replacing an imported cell; its original address is retained. */
export const Cell = component<ElementProps & { address?: Address; value: Value; format?: CellFormat; hyperlink?: string }>("cell");
export const Merge = component<ElementProps & { range: Range }>("merge");
export const Row = component<ElementProps & { row: number; height: number }>("row");
export const Column = component<ElementProps & { column: number; width: number }>("column");
/** Imported rules retain their range when range is omitted. */
export const ConditionalFormat = component<ElementProps & { range?: Range; rule: ConditionalRule }>("conditional-format");
export const DataValidation = component<ElementProps & { range?: Range; kind: ValidationKind; operator?: Comparison; formulas: string[]; allowBlank?: boolean; prompt?: string; error?: string }>("data-validation");
export const Chart = component<ChartProps>("chart");
