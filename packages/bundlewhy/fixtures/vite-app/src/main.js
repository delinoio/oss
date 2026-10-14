// bundlewhy acceptance fixture. Each import pairs an optimization blocker with
// a counterexample where the blocker is absent.
import { unusedEffectful } from "./effects/register.js";
import { unusedPure } from "./pure/index.js";
import { formatV1 } from "../vendor/dup-v1/index.js";
import { formatV2 } from "../vendor/dup-v2/index.js";
import { single } from "../vendor/single/index.js";
import { singleAgain } from "../vendor/single/again.js";
import legacy from "./legacy/cjs.cjs";
import { esmValue } from "./esm/value.js";
import { evaluate } from "./dynamic/evaluate.js";
import { plainAdd } from "./dynamic/plain.js";
import { shared } from "./shared/static-and-dynamic.js";

if (process.env.NODE_ENV !== "production") {
  console.log("development-only branch");
}
if (process.env.BUNDLEWHY_FIXTURE_FLAG === "on") {
  console.log("flag branch");
}

console.log(formatV1(1), formatV2(2), single(), singleAgain());
console.log(legacy.legacyValue, esmValue, evaluate("1 + 1"), plainAdd(1, 2));
console.log(shared());

import("./lazy/a.js").then((m) => console.log(m.a()));
import("./lazy/b.js").then((m) => console.log(m.b()));
import("./shared/static-and-dynamic.js").then((m) => console.log(m.shared()));
