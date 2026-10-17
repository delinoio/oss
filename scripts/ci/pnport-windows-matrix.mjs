// SPDX-License-Identifier: Apache-2.0
import { developmentMatrix } from "../../packages/pnport/scripts/native-matrix.mjs";

const include = developmentMatrix.include.filter(({ target }) =>
  target.endsWith("-pc-windows-msvc"),
);
if (include.length !== 2)
  throw new Error("Expected both native Windows development targets");
console.log(`matrix=${JSON.stringify({ include })}`);
