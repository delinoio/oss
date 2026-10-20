// SPDX-License-Identifier: Apache-2.0
import { createSignal } from "solid-js/dist/solid.js";
import { createRenderer } from "solid-js/universal";

type Node = { text: string; children: Node[]; parent?: Node };
const updates: string[] = [];
const renderer = createRenderer<Node>({
  createElement: () => ({ text: "", children: [] }),
  createTextNode: (text) => {
    updates.push(text);
    return { text, children: [] };
  },
  isTextNode: (node) => node.children.length === 0,
  replaceText: (node, text) => {
    node.text = text;
    updates.push(text);
  },
  setProperty: () => {},
  insertNode: (parent, node, anchor) => {
    node.parent = parent;
    const index = anchor ? parent.children.indexOf(anchor) : -1;
    if (index < 0) parent.children.push(node);
    else parent.children.splice(index, 0, node);
  },
  removeNode: (parent, node) => {
    parent.children.splice(parent.children.indexOf(node), 1);
    node.parent = undefined;
  },
  getParentNode: (node) => node.parent,
  getFirstChild: (node) => node.children[0],
  getNextSibling: (node) =>
    node.parent?.children[node.parent.children.indexOf(node) + 1],
});
const root: Node = { text: "", children: [] };
let update = () => {};
const dispose = renderer.render(() => {
  const [count, setCount] = createSignal(0);
  const element = renderer.createElement("div");
  renderer.insert(element, () => String(count()));
  update = () => {
    setCount(1);
  };
  return element;
}, root);
update();
if (updates.join(",") !== "0,1" || root.children[0].children[0].text !== "1")
  throw new Error("renderer update failed");
dispose();
console.log("typenative-solid-renderer-v1:" + updates.join(","));
