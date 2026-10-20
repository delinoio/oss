// SPDX-License-Identifier: Apache-2.0
declare function nativeInit(html: string): void;
declare function nativeText(id: number, text: string): void;
declare function nativeSnapshot(frame: number): number;

type Signal = { get: () => number; set: (value: number) => void };
type Props = { id?: number; text?: () => string; onClick?: () => void };
type View = { tag: string; props: Props; children: View[] };
const effects: (() => void)[] = [];

// This experiment reruns every effect synchronously; it has no dependency tracker.
function signal(initial: number): Signal {
  let value = initial;
  return {
    get: () => value,
    set: (next: number) => {
      value = next;
      for (let i = 0; i < effects.length; i++) effects[i]();
    },
  };
}
function h(tag: string, props: Props | null, ...children: View[]): View {
  return { tag, props: props || {}, children };
}
function escapeText(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}
function html(view: View): string {
  const id = view.props.id === undefined ? "" : ' id="n' + view.props.id + '"';
  let result = "<" + view.tag + id + ">";
  // Use a narrowed local: ScriptC 0.3.5 cannot lower optional function-property calls.
  const text = view.props.text;
  if (text) result += escapeText(text());
  for (let i = 0; i < view.children.length; i++)
    result += html(view.children[i]);
  return result + "</" + view.tag + ">";
}
function bind(view: View): void {
  const id = view.props.id;
  const text = view.props.text;
  if (id !== undefined && text) {
    const effect = () => nativeText(id, text());
    effects.push(effect);
    effect();
  }
  for (let i = 0; i < view.children.length; i++) bind(view.children[i]);
}
function click(view: View, id: number): boolean {
  const handler = view.props.onClick;
  if (view.props.id === id && handler) {
    handler();
    return true;
  }
  for (let i = 0; i < view.children.length; i++) {
    if (click(view.children[i], id)) return true;
  }
  return false;
}

const count = signal(0);
const app = (
  <main>
    <h1 text={() => "Custom TSX native counter"} />
    <p id={1} text={() => String(count.get())} />
    <button
      id={2}
      text={() => "Increment"}
      onClick={() => count.set(count.get() + 1)}
    />
  </main>
);
nativeInit(
  "<!doctype html><html><head><style>body{margin:24px;background:white;color:black;font:24px sans-serif}h1{font-size:26px;margin:0}p{font-size:80px;margin:12px 0}button{padding:12px}</style></head><body>" +
    html(app) +
    "</body></html>",
);
bind(app);
if (count.get() !== 0 || nativeSnapshot(0) !== 1)
  throw new Error("initial frame failed");
if (!click(app, 2)) throw new Error("handler not found");
if (count.get() !== 1 || nativeSnapshot(1) !== 1)
  throw new Error("updated frame failed");
console.log("typenative-custom-tsx-v1:0,1");
