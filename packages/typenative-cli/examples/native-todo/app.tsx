// SPDX-License-Identifier: Apache-2.0
declare function nativeRun(
  html: string,
  handler: (action: number, input: string) => void,
): void;
declare function nativeUpdate(target: number, value: string): void;
declare function nativeClearInput(): void;

type Props = {
  id?: string;
  className?: string;
  type?: string;
  placeholder?: string;
  label?: string;
  checked?: boolean;
  ariaHidden?: boolean;
  action?: number;
  text?: () => string;
  onClick?: () => void;
  children?: View[];
};
type View = { tag: string; props: Props; children: View[] };
type Todo = { id: number; title: string; done: boolean };
type Signal = { get: () => number; set: (value: number) => void };
const effects: (() => void)[] = [];

// This small custom runtime deliberately has synchronous effects and intrinsic
// TSX only. It does not claim component reconciliation or dependency tracking.
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
  const attributes = props || {};
  return { tag, props: attributes, children: attributes.children || children };
}
function escapeText(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}
function attribute(name: string, value: string | undefined): string {
  return value === undefined ? "" : " " + name + '="' + escapeText(value) + '"';
}
function html(view: View): string {
  const p = view.props;
  let result =
    "<" +
    view.tag +
    attribute("id", p.id) +
    attribute("class", p.className) +
    attribute("type", p.type) +
    attribute("placeholder", p.placeholder) +
    attribute("aria-label", p.label);
  if (p.action !== undefined)
    result += attribute("data-action", String(p.action));
  if (p.checked) result += " checked";
  if (p.ariaHidden) result += ' aria-hidden="true"';
  result += ">";
  if (view.tag === "input") return result;
  const text = p.text;
  if (text) result += escapeText(text());
  for (let i = 0; i < view.children.length; i++)
    result += html(view.children[i]);
  return result + "</" + view.tag + ">";
}
function click(view: View, action: number): boolean {
  const handler = view.props.onClick;
  if (view.props.action === action && handler) {
    handler();
    return true;
  }
  for (let i = 0; i < view.children.length; i++) {
    if (click(view.children[i], action)) return true;
  }
  return false;
}

const todos: Todo[] = [
  { id: 0, title: "Try TypeNative", done: false },
  { id: 1, title: "Build a todo app", done: false },
  { id: 2, title: "Create a project", done: true },
];
let nextId = 3;
let inputValue = "";
const revision = signal(0);
function changed(): void {
  revision.set(revision.get() + 1);
}
function remaining(): string {
  let count = 0;
  for (let i = 0; i < todos.length; i++) if (!todos[i].done) count++;
  return String(count) + " remaining";
}
function add(): void {
  const title = inputValue.trim();
  if (title.length === 0) return;
  todos.push({ id: nextId++, title, done: false });
  nativeClearInput();
  nativeUpdate(3, "What needs to be done?");
  changed();
}
function remove(id: number): void {
  for (let i = 0; i < todos.length; i++) {
    if (todos[i].id === id) {
      todos.splice(i, 1);
      changed();
      return;
    }
  }
}
function rows(): View[] {
  const result: View[] = [];
  for (let i = 0; i < todos.length; i++) {
    const todo = todos[i];
    result.push(
      <li className={todo.done ? "row done" : "row"}>
        <input
          type={"checkbox"}
          checked={todo.done}
          label={"Complete " + todo.title}
          action={100 + todo.id * 2}
          onClick={() => {
            todo.done = !todo.done;
            changed();
          }}
        />
        <span className={"title"} text={() => todo.title} />
        <button
          type={"button"}
          className={"delete"}
          label={"Delete " + todo.title}
          action={101 + todo.id * 2}
          text={() => "Delete"}
          onClick={() => remove(todo.id)}
        />
      </li>,
    );
  }
  if (result.length === 0)
    result.push(
      <li
        className={"empty"}
        text={() => "No tasks yet. Add your first task above."}
      />,
    );
  return result;
}
let list = <ul id={"list"} children={rows()} />;
// The pinned Blitz revision does not paint input placeholders. Retain this
// pointer-transparent hint until upgrading to a revision with native placeholder
// painting. Input events and successful submissions update its text explicitly.
const app = (
  <main>
    <h1 text={() => "Todo"} />
    <p id={"count"} text={remaining} />
    <div className={"entry"}>
      <div className={"field"}>
        <input
          id={"new-todo"}
          type={"text"}
          placeholder={"What needs to be done?"}
          label={"New task"}
        />
        <span
          id={"input-hint"}
          className={"hint"}
          ariaHidden={true}
          text={() => "What needs to be done?"}
        />
      </div>
      <button
        type={"button"}
        className={"add"}
        action={1}
        text={() => "Add"}
        onClick={add}
      />
    </div>
  </main>
);
// Keep the entry controls mounted while replacing only the list's children.
app.children.push(list);
effects.push(() => {
  list = <ul id={"list"} children={rows()} />;
  let contents = "";
  for (let i = 0; i < list.children.length; i++)
    contents += html(list.children[i]);
  nativeUpdate(1, contents);
  nativeUpdate(2, remaining());
});
const css = `
  *{box-sizing:border-box}html{height:100%;background:#f7f8fa;color:#191f28;font:16px sans-serif}
  body{margin:0;padding:28px}main{max-width:720px;margin:0 auto}h1{font-size:36px;margin:8px 0 4px;font-weight:700}
  #count{color:#6b7684;margin:0 0 26px}.entry{display:flex;gap:10px;margin-bottom:22px}
  .field{flex:1;min-width:0;position:relative;display:flex}
  .hint{position:absolute;left:14px;right:10px;top:14px;color:#8b95a1;pointer-events:none;white-space:nowrap;overflow:hidden}
  input[type=text]{min-width:0;flex:1;padding:13px 14px;font:16px sans-serif;background:white;border:1px solid #b0b8c1;border-radius:9px;color:#191f28}
  button{font:16px sans-serif;cursor:pointer;border:0;border-radius:9px;padding:13px 20px}
  .add{color:white;background:#3182f6}.add:hover{background:#1b64da}
  :focus-visible{outline:2px solid #3182f6;outline-offset:3px}
  #list{list-style:none;padding:0;margin:0;display:flex;flex-direction:column;gap:12px}
  .row{display:flex;align-items:center;gap:14px;min-height:60px;padding:12px 16px;border:1px solid #e5e8eb;border-radius:10px;background:white}
  /* Pinned Blitz paints checkboxes with color; remove the color override once it honors accent-color. */
  .row input{width:21px;height:21px;flex-shrink:0;cursor:pointer;accent-color:#3182f6;color:#3182f6}
  .title{flex:1;min-width:0;overflow-wrap:anywhere}.done .title{color:#8b95a1;text-decoration:line-through}
  .delete{background:transparent;color:#e5243b;font-size:14px;padding:9px 4px}.delete:hover{background:#fff0f2}
  .empty{padding:28px 12px;text-align:center;color:#6b7684}
  @media(max-width:420px){body{padding:20px}.row{gap:10px;padding:12px}.add{padding:13px 16px}}
`;
nativeRun(
  '<!doctype html><html lang="en"><head><meta charset="utf-8"><style>' +
    css +
    "</style></head><body>" +
    html(app) +
    "</body></html>",
  (action: number, input: string) => {
    inputValue = input;
    if (action === 2)
      nativeUpdate(3, input.length === 0 ? "What needs to be done?" : "");
    else if (action === 1) click(app, action);
    else click(list, action);
  },
);
