// SPDX-License-Identifier: Apache-2.0
// Select the published client runtime explicitly. The Node default is SSR.
import {
  createRoot,
  createSignal,
  createRenderEffect,
} from "solid-js/dist/solid.js";

const values: number[] = [];
let update = () => {};
let cleanup = () => {};
createRoot((dispose) => {
  const [count, setCount] = createSignal(0);
  createRenderEffect(() => {
    values.push(count());
  });
  update = () => {
    setCount(1);
  };
  cleanup = dispose;
});
// Update after the root's initial batch has completed.
update();
cleanup();
if (values.join(",") !== "0,1") throw new Error("signal effect failed");
console.log("typenative-solid-signal-v1:" + values.join(","));
