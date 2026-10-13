// SPDX-License-Identifier: Apache-2.0
import { useRef, useState } from "react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  SessionService,
  SessionQuery,
  SkillService,
  SkillProvenance,
  newRequestId,
} from "@delinoio/delidev-api-client";
import {
  analyzeSkillTokens,
  MAX_PAINTED_SKILL_RANGES,
  skillToken,
  skillRanges,
  editedBindings,
  useSkillCompletion,
  type SkillTokenBinding,
} from "./skill-completion";
import {
  AppearanceProvider,
  Theme,
  type AppearanceBridge,
  type AppearanceSnapshot,
} from "./appearance";
import { defaultPreferences } from "./appearance-preferences";
import { MutationIntents, useRetainedMutation } from "./mutation";
import { SupportedLanguage, i18n } from "./localization";
const machine = newRequestId(),
  agent = newRequestId(),
  inventory = newRequestId(),
  worker = newRequestId();
const entries = ["add-issue", "add-note"].map((name) => ({
  name,
  description: `${name} fixture`,
  provenance: SkillProvenance.USER,
  selection: {
    $typeName: "delidev.v1.SkillSelection" as const,
    inventoryId: inventory,
    workerDeviceId: worker,
    skillId: newRequestId(),
    contentRevision: "a".repeat(64),
  },
}));
function Composer({
  runner = machine,
  locked = false,
  enabled = true,
  retainTransportContext = false,
  send,
}: {
  runner?: string;
  locked?: boolean;
  enabled?: boolean;
  retainTransportContext?: boolean;
  send: (value: unknown) => void;
}) {
  const [value, change] = useState("");
  const textarea = useRef<HTMLTextAreaElement>(null);
  const skills = useSkillCompletion({
    value,
    change,
    textarea,
    machineId: runner,
    agentId: agent,
    disabled: locked,
    enabled,
    retainTransportContext,
  });
  return (
    <>
      <fieldset disabled={locked}>
        {skills.wrap(
          <textarea
            aria-label="Message"
            ref={textarea}
            value={value}
            onChange={(e) =>
              skills.onChange(e.target.value, e.target.selectionStart)
            }
            onSelect={skills.onSelect}
            onKeyDown={skills.onKeyDown}
            onCompositionStart={skills.onCompositionStart}
            onCompositionEnd={skills.onCompositionEnd}
            {...skills.attributes}
          />,
        )}
        {skills.list}
        {skills.warning}
      </fieldset>
      <button
        disabled={skills.blocked}
        onClick={() => send({ value, skills: skills.selections })}
      >
        Send
      </button>
    </>
  );
}
function countStatus(view: { container: HTMLElement }) {
  return view.container.querySelector<HTMLElement>(
    ".skill-textarea > [role='status']",
  )!;
}
function fixture() {
  const send = vi.fn(),
    read = vi.fn(async () => ({ skills: entries }));
  const transport = createRouterTransport((router) =>
    router.service(SkillService, { listSkills: read }),
  );
  const client = new QueryClient();
  const view = (runner = machine, locked = false) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <Composer runner={runner} locked={locked} send={send} />
      </QueryClientProvider>
    </TransportProvider>
  );
  return { send, read, view, client };
}
it("finds whitespace-delimited caret tokens without consuming surrounding Unicode", () => {
  expect(skillToken("한글\n$add-iss trailing", 11)).toEqual({
    start: 3,
    end: 11,
    prefix: "add-iss",
  });
  expect(skillToken("email$add", 9)).toBeUndefined();
});
it("binds keyboard selection separately, replaces only the token and does not send", async () => {
  const f = fixture();
  render(f.view());
  const input = screen.getByRole("textbox");
  fireEvent.change(input, {
    target: { value: "한글\n$add-iss", selectionStart: 11 },
  });
  await screen.findByRole("option");
  fireEvent.keyDown(input, { key: "Enter" });
  expect((input as HTMLTextAreaElement).value).toBe("한글\n$add-issue");
  expect(f.send).not.toHaveBeenCalled();
  fireEvent.click(screen.getByText("Send"));
  expect(f.send.mock.calls[0]![0]).toMatchObject({
    skills: [entries[0]!.selection],
  });
});
it("removes an edited binding and requires reselection on Runner changes", async () => {
  const f = fixture();
  const view = render(f.view());
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$add-iss", selectionStart: 8 } });
  await screen.findByRole("option");
  fireEvent.keyDown(input, { key: "Tab" });
  view.rerender(f.view(newRequestId()));
  await screen.findByText(
    "The selected skills need reselection before sending.",
  );
  expect((screen.getByText("Send") as HTMLButtonElement).disabled).toBe(true);
  fireEvent.change(input, { target: { value: "manual", selectionStart: 6 } });
  await waitFor(() =>
    expect((screen.getByText("Send") as HTMLButtonElement).disabled).toBe(
      false,
    ),
  );
  fireEvent.click(screen.getByText("Send"));
  expect(f.send.mock.calls[0]![0]).toMatchObject({ skills: [] });
});
it("does not select or submit during IME composition and Escape preserves text", async () => {
  const f = fixture();
  render(f.view());
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$add-iss", selectionStart: 8 } });
  await screen.findByRole("option");
  fireEvent.compositionStart(input);
  fireEvent.keyDown(input, { key: "Enter", isComposing: true });
  expect((input as HTMLTextAreaElement).value).toBe("$add-iss");
  fireEvent.compositionEnd(input);
  fireEvent.keyDown(input, { key: "Escape" });
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(f.send).not.toHaveBeenCalled();
});
it("keeps manually typed tokens unbound and supports localized loading states", async () => {
  await i18n.changeLanguage(SupportedLanguage.Korean);
  const f = fixture();
  render(f.view());
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$unknown", selectionStart: 8 } });
  await screen.findByText("일치하는 스킬이 없습니다.");
  fireEvent.click(screen.getByText("Send"));
  expect(f.send.mock.calls[0]![0]).toMatchObject({ skills: [] });
  await i18n.changeLanguage(SupportedLanguage.English);
});
it("keeps bindings shifted by outside edits but removes changed selected tokens", () => {
  const binding = {
    start: 2,
    end: 12,
    token: "$add-issue",
    selection: entries[0]!.selection,
    stale: false,
  };
  expect(
    editedBindings("a $add-issue", "prefix a $add-issue", [binding])[0]?.start,
  ).toBe(9);
  expect(editedBindings("a $add-issue", "a $other", [binding])).toEqual([]);
});

it("includes the remainder of the caret token without matching it as a prefix", () => {
  expect(skillToken("before $add-issue after", 15)).toEqual({
    start: 7,
    end: 17,
    prefix: "add-iss",
  });
});

it("does not accept IME commit Enter, repeated keys or AltGraph", async () => {
  const f = fixture();
  render(f.view());
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$add-iss", selectionStart: 8 } });
  await screen.findByRole("option");
  for (const event of [
    { key: "Enter", keyCode: 229, isComposing: false },
    { key: "Enter", repeat: true },
    { key: "Enter", modifierAltGraph: true },
  ]) {
    fireEvent.keyDown(input, event);
    expect((input as HTMLTextAreaElement).value).toBe("$add-iss");
  }
  expect(f.send).not.toHaveBeenCalled();
});

it("drops a binding when typing at either token boundary but preserves whitespace", () => {
  const binding = {
    start: 2,
    end: 7,
    token: "$same",
    selection: entries[0]!.selection,
    stale: false,
  };
  for (const value of ["a $samex", "a $same한글", "a x$same"]) {
    expect(editedBindings("a $same", value, [binding])).toEqual([]);
  }
  expect(editedBindings("a $same", "a $same ", [binding])).toHaveLength(1);
  expect(editedBindings("a $same", "a  $same", [binding])).toHaveLength(1);
});

it("rejects native fieldset-locked completion clicks and keyboard edits before React settlement", async () => {
  const f = fixture();
  const view = render(f.view());
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$ad", selectionStart: 3 } });
  const option = await screen.findByRole("option", { name: /add-issue/ });
  view.container.querySelector("fieldset")!.disabled = true;
  fireEvent.click(option);
  fireEvent.keyDown(input, { key: "Enter" });
  fireEvent.change(input, { target: { value: "changed", selectionStart: 7 } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  expect(f.send).toHaveBeenLastCalledWith({ value: "$ad", skills: [] });
});
it("keeps exact draft and accepted bindings through pending or uncertain composer locks", async () => {
  const f = fixture();
  const view = render(f.view());
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$ad", selectionStart: 3 } });
  fireEvent.click(await screen.findByRole("option", { name: /add-issue/ }));
  fireEvent.change(input, {
    target: { value: "$add-issue $ad", selectionStart: 14 },
  });
  await screen.findByRole("option", { name: /add-note/ });
  view.rerender(f.view(machine, true));
  expect(
    view.container.querySelector(".skill-token-selected")?.textContent,
  ).toBe("$add-issue");
  expect(screen.queryByRole("listbox")).toBeNull();
  fireEvent.keyDown(input, { key: "Enter" });
  fireEvent.change(input, { target: { value: "changed", selectionStart: 7 } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  expect(f.send).toHaveBeenLastCalledWith({
    value: "$add-issue $ad",
    skills: [entries[0]!.selection],
  });
  view.rerender(f.view());
  await screen.findByRole("option", { name: /add-note/ });
  expect((input as HTMLTextAreaElement).value).toBe("$add-issue $ad");
});

function AcceptedComposer() {
  const [value, change] = useState("");
  const textarea = useRef<HTMLTextAreaElement>(null);
  const mutation = useRetainedMutation(
    "skills-accepted-fixture",
    SessionQuery.enqueueInput,
    () => {
      change("");
      skills.clearAccepted();
    },
  );
  const skills = useSkillCompletion({
    value,
    change,
    textarea,
    machineId: machine,
    agentId: agent,
    disabled: mutation.busy || mutation.uncertain,
  });
  return (
    <>
      {skills.wrap(
        <textarea
          aria-label="Accepted message"
          disabled={mutation.busy || mutation.uncertain}
          ref={textarea}
          value={value}
          onChange={(e) =>
            skills.onChange(e.target.value, e.target.selectionStart)
          }
        />,
      )}
      {skills.list}
      <output data-selected>{skills.selections.length}</output>
      <button
        onClick={() =>
          void mutation.send({
            requestId: newRequestId(),
            sessionId: newRequestId(),
            documentJson: new Uint8Array(),
          })
        }
      >
        Enqueue fixture
      </button>
    </>
  );
}
it("clears accepted bindings in the real retained-mutation callback before the pending render unlocks", async () => {
  let resolve!: () => void;
  const pending = new Promise<void>((done) => {
    resolve = done;
  });
  const transport = createRouterTransport((router) => {
    router.service(SkillService, {
      listSkills: async () => ({ skills: entries }),
    });
    router.service(SessionService, {
      enqueueInput: async () => {
        await pending;
        return {};
      },
    });
  });
  const view = render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={new QueryClient()}>
        <MutationIntents>
          <AcceptedComposer />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$ad", selectionStart: 3 } });
  fireEvent.click(await screen.findByRole("option", { name: /add-issue/ }));
  expect(view.container.querySelector("[data-selected]")!.textContent).toBe(
    "1",
  );
  expect(
    view.container.querySelector(".skill-token-selected")?.textContent,
  ).toBe("$add-issue");
  fireEvent.click(screen.getByRole("button", { name: "Enqueue fixture" }));
  await waitFor(() =>
    expect((input as HTMLTextAreaElement).disabled).toBe(true),
  );
  expect(
    view.container.querySelector(".skill-token-selected")?.textContent,
  ).toBe("$add-issue");
  resolve();
  await waitFor(() => expect((input as HTMLTextAreaElement).value).toBe(""));
  expect(view.container.querySelector(".skill-token-selected")).toBeNull();
  fireEvent.change(input, {
    target: { value: "$add-issue", selectionStart: 10 },
  });
  expect(view.container.querySelector("[data-selected]")!.textContent).toBe(
    "0",
  );
});

function RestoredComposer({
  runner,
  reject = false,
}: {
  runner: string;
  reject?: boolean;
}) {
  const [value, setValue] = useState("$add-issue");
  const textarea = useRef<HTMLTextAreaElement>(null);
  const skills = useSkillCompletion({
    value,
    change: (next) => {
      if (reject) return false;
      setValue(next);
      return true;
    },
    textarea,
    machineId: runner,
    agentId: agent,
    initialBindings: [
      {
        start: 0,
        end: 10,
        token: "$add-issue",
        selection: entries[0]!.selection,
        stale: false,
        context: `${machine}:${agent}::`,
      },
    ],
  });
  return (
    <>
      <textarea
        ref={textarea}
        value={value}
        onChange={(event) =>
          skills.onChange(event.target.value, event.target.selectionStart)
        }
      />
      {skills.warning}
      <button disabled={skills.blocked}>Restored send</button>
      <output>{skills.selections.length}</output>
    </>
  );
}
it("blocks a retained binding during unresolved and changed context until explicit token removal", async () => {
  const client = new QueryClient();
  const transport = createRouterTransport((router) =>
    router.service(SkillService, {
      listSkills: async () => ({ skills: entries }),
    }),
  );
  const tree = (runner: string) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <RestoredComposer runner={runner} />
      </QueryClientProvider>
    </TransportProvider>
  );
  const view = render(tree(""));
  expect(screen.getByRole("button", { name: "Restored send" })).toHaveProperty(
    "disabled",
    true,
  );
  view.rerender(tree(machine));
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Restored send" }),
    ).toHaveProperty("disabled", false),
  );
  view.rerender(tree(newRequestId()));
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Restored send" }),
    ).toHaveProperty("disabled", true),
  );
  fireEvent.change(screen.getByRole("textbox"), {
    target: { value: "$plain", selectionStart: 6 },
  });
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Restored send" }),
    ).toHaveProperty("disabled", false),
  );
  expect(
    screen.queryByText("The selected skills need reselection before sending."),
  ).toBeNull();
});
it("retains text and original bindings atomically when draft growth is rejected", () => {
  const transport = createRouterTransport((router) =>
    router.service(SkillService, {
      listSkills: async () => ({ skills: entries }),
    }),
  );
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={new QueryClient()}>
        <RestoredComposer runner={machine} reject />
      </QueryClientProvider>
    </TransportProvider>,
  );
  fireEvent.change(screen.getByRole("textbox"), {
    target: { value: "rejected", selectionStart: 8 },
  });
  expect(screen.getByRole("textbox")).toHaveProperty("value", "$add-issue");
  expect(screen.getByText("1")).toBeDefined();
  expect(screen.getByRole("button", { name: "Restored send" })).toHaveProperty(
    "disabled",
    false,
  );
});

function HistorySkillComposer({ locked = false }: { locked?: boolean }) {
  const [value, change] = useState("$add-issue");
  const textarea = useRef<HTMLTextAreaElement>(null);
  const skills = useSkillCompletion({
    value,
    change,
    textarea,
    machineId: machine,
    agentId: agent,
    disabled: locked,
    initialBindings: [
      {
        start: 0,
        end: 10,
        token: "$add-issue",
        selection: entries[0]!.selection,
        stale: false,
        context: `${machine}:${agent}::`,
      },
    ],
  });
  return (
    <>
      <textarea
        aria-label="History skill draft"
        ref={textarea}
        disabled={locked}
        value={value}
        onChange={(event) =>
          skills.onChange(event.target.value, event.target.selectionStart)
        }
      />
      <output data-history-selections>{skills.selections.length}</output>
      <button onClick={() => skills.replaceUnbound("$add-issue", 0)}>
        Recall text only
      </button>
    </>
  );
}
it("recalling identical text drops typed skill authority and respects the creation lock", () => {
  const transport = createRouterTransport((router) =>
    router.service(SkillService, {
      listSkills: async () => ({ skills: entries }),
    }),
  );
  const client = new QueryClient();
  const tree = (locked: boolean) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <HistorySkillComposer locked={locked} />
      </QueryClientProvider>
    </TransportProvider>
  );
  const view = render(tree(true));
  fireEvent.click(screen.getByRole("button", { name: "Recall text only" }));
  expect(
    view.container.querySelector("[data-history-selections]")!.textContent,
  ).toBe("1");
  view.rerender(tree(false));
  fireEvent.click(screen.getByRole("button", { name: "Recall text only" }));
  expect(
    screen.getByRole("textbox", { name: "History skill draft" }),
  ).toHaveProperty("value", "$add-issue");
  expect(
    view.container.querySelector("[data-history-selections]")!.textContent,
  ).toBe("0");
});

it("retains original option text with description before the separate provenance badge", async () => {
  const long = {
    ...entries[0]!,
    name: "길고긴스킬이름-long-name",
    description: "Original 한글 description ".repeat(100),
    provenance: SkillProvenance.PROJECT,
  };
  const transport = createRouterTransport((router) =>
    router.service(SkillService, {
      listSkills: async () => ({
        skills: [long, { ...entries[1]!, description: "" }],
      }),
    }),
  );
  const view = render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={new QueryClient()}>
        <Composer send={vi.fn()} />
      </QueryClientProvider>
    </TransportProvider>,
  );
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$", selectionStart: 1 } });
  const option = await screen.findByRole("option", {
    name: new RegExp(long.name),
  });
  expect([...option.children].map((child) => child.className)).toEqual([
    "skill-completion-name",
    "skill-completion-description",
    "skill-completion-provenance",
  ]);
  expect(option.children[1]!.textContent).toBe(long.description);
  expect(option.children[2]!.textContent).toBe("Project");
  expect(screen.getAllByRole("option")).toHaveLength(2);
  expect(
    view.container.querySelectorAll(".skill-completion-description")[0]!
      .textContent,
  ).toBe("");
  expect(input.getAttribute("aria-activedescendant")).toBe(
    screen.getAllByRole("option")[0]!.id,
  );
});

it.each([1, 2])(
  "scrolls only the completion container at scale %s and retains textarea focus",
  async (scale) => {
    const f = fixture();
    const view = render(f.view());
    const input = screen.getByRole("textbox");
    input.focus();
    fireEvent.change(input, { target: { value: "$add", selectionStart: 4 } });
    await screen.findAllByRole("option");
    const container =
      view.container.querySelector<HTMLElement>(".skill-completion")!;
    Object.defineProperty(container, "clientHeight", { value: 100 });
    Object.defineProperty(container, "clientTop", { value: 1 });
    Object.defineProperty(container, "offsetHeight", { value: 102 });
    vi.spyOn(container, "getBoundingClientRect").mockReturnValue({
      top: 10,
      bottom: 10 + 102 * scale,
      height: 102 * scale,
    } as DOMRect);
    const rows = screen.getAllByRole("option");
    vi.spyOn(rows[0]!, "getBoundingClientRect").mockReturnValue({
      top: 10 - 39 * scale,
      bottom: 10 + scale,
    } as DOMRect);
    vi.spyOn(rows[1]!, "getBoundingClientRect").mockReturnValue({
      top: 10 + 101 * scale,
      bottom: 10 + 141 * scale,
    } as DOMRect);
    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(container.scrollTop).toBe(40);
    expect(document.activeElement).toBe(input);
    expect(input.getAttribute("aria-activedescendant")).toBe(rows[1]!.id);
    fireEvent.keyDown(input, { key: "ArrowUp" });
    expect(container.scrollTop).toBe(0);
    expect(document.activeElement).toBe(input);
    expect(f.send).not.toHaveBeenCalled();
  },
);

it("colors exact complete tokens after a successful read without binding text and suppresses overlays during IME", async () => {
  const f = fixture();
  const view = render(f.view());
  const input = screen.getByRole("textbox");
  const draft = "한글\n$add-issue $removed-skill $removed-skill";
  fireEvent.change(input, {
    target: { value: draft, selectionStart: draft.length },
  });
  await waitFor(() =>
    expect(
      view.container.querySelectorAll(".skill-token-unavailable"),
    ).toHaveLength(2),
  );
  expect(
    [...view.container.querySelectorAll(".skill-token-unavailable")].map(
      (node) => node.textContent,
    ),
  ).toEqual(["$removed-skill", "$removed-skill"]);
  expect(
    screen.getByRole("textbox", {
      description: "2 skill tokens are unavailable in this scope.",
    }),
  ).toBe(input);
  fireEvent.keyDown(input, { key: "Escape" });
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(
    view.container.querySelectorAll(".skill-token-unavailable"),
  ).toHaveLength(2);
  fireEvent.click(screen.getByText("Send"));
  expect(f.send).toHaveBeenLastCalledWith({ value: draft, skills: [] });
  fireEvent.compositionStart(input);
  expect(view.container.querySelector(".skill-text-overlay")).toBeNull();
  fireEvent.compositionEnd(input);
  await waitFor(() =>
    expect(
      view.container.querySelectorAll(".skill-token-unavailable"),
    ).toHaveLength(2),
  );
  fireEvent.change(input, { target: { value: "$ad", selectionStart: 3 } });
  await screen.findAllByRole("option");
  expect(view.container.querySelector(".skill-text-overlay")).toBeNull();
  expect(skillRanges("$ email$bad $bad$other\n$valid $valid")).toEqual([
    { start: 23, end: 29, prefix: "valid" },
    { start: 30, end: 36, prefix: "valid" },
  ]);
});
it("keeps a dismissed valid caret prefix ordinary and still flags confirmed-absent tokens elsewhere", async () => {
  const f = fixture();
  const view = render(f.view());
  const input = screen.getByRole("textbox") as HTMLTextAreaElement;
  input.focus();
  fireEvent.change(input, { target: { value: "$add", selectionStart: 4 } });
  await screen.findAllByRole("option");
  fireEvent.keyDown(input, { key: "Escape" });
  expect(screen.queryByRole("listbox")).toBeNull();
  await act(async () => {
    await f.client.invalidateQueries();
  });
  await waitFor(() => expect(f.client.isFetching()).toBe(0));
  expect(input.value).toBe("$add");
  expect(input.selectionStart).toBe(4);
  expect(document.activeElement).toBe(input);
  expect(view.container.querySelector(".skill-token-unavailable")).toBeNull();
  expect(view.container.querySelector(".skill-text-overlay")).toBeNull();
  expect(input.hasAttribute("aria-describedby")).toBe(false);
  expect(countStatus(view).textContent).toBe("");
  // A complete absent token elsewhere is still flagged while the prefix stays ordinary.
  const draft = "$removed $add";
  fireEvent.change(input, {
    target: { value: draft, selectionStart: draft.length },
  });
  await screen.findAllByRole("option");
  fireEvent.keyDown(input, { key: "Escape" });
  expect(screen.queryByRole("listbox")).toBeNull();
  await waitFor(() =>
    expect(
      [...view.container.querySelectorAll(".skill-token-unavailable")].map(
        (node) => node.textContent,
      ),
    ).toEqual(["$removed"]),
  );
  expect(
    screen.getByRole("textbox", {
      description: "1 skill token is unavailable in this scope.",
    }),
  ).toBe(input);
  // Moving the caret away makes the dismissed prefix a complete absent token.
  fireEvent.select(input, { target: { selectionStart: 0 } });
  await waitFor(() =>
    expect(
      [...view.container.querySelectorAll(".skill-token-unavailable")].map(
        (node) => node.textContent,
      ),
    ).toEqual(["$removed", "$add"]),
  );
  expect(input.value).toBe(draft);
});
class CountingSet extends Set<string> {
  lookups = 0;
  override has(value: string) {
    this.lookups++;
    return super.has(value);
  }
}
it("analyzes absence with one exact-name Set lookup per complete token and a bounded caret check", () => {
  const names = new CountingSet(["add-issue", "add-note"]);
  const draft = "$add-issue $gone $add-no $gone\n$add-note $add-";
  const analysis = analyzeSkillTokens(draft, {
    names,
    caret: { start: draft.length - 5, prefixAvailable: true },
  });
  // The trailing partial token is the caret prefix and the third is absent.
  expect(names.lookups).toBe(skillRanges(draft).length);
  expect(analysis.unavailableCount).toBe(3);
  expect(analysis.unavailable.map((range) => range.prefix)).toEqual([
    "gone",
    "add-no",
    "gone",
  ]);
  expect(analysis.selected).toEqual([]);
  // Without a caret exemption or confirmed inventory the result is explicit.
  expect(analyzeSkillTokens(draft, { names }).unavailableCount).toBe(4);
  expect(analyzeSkillTokens(draft, {}).unavailableCount).toBe(0);
});
it("keeps the exact count for 87,000 missing tokens while materializing no ranges beyond the budget", () => {
  const draft = "$a ".repeat(87_000);
  expect(new TextEncoder().encode(draft).length).toBe(261_000);
  const names = new CountingSet(entries.map((entry) => entry.name));
  const analysis = analyzeSkillTokens(draft, { names });
  expect(analysis.unavailableCount).toBe(87_000);
  expect(names.lookups).toBe(87_000);
  expect(analysis.unavailable).toEqual([]);
  expect(analysis.selected).toEqual([]);
  const budget = "$a ".repeat(MAX_PAINTED_SKILL_RANGES);
  const atBudget = analyzeSkillTokens(budget, { names });
  expect(atBudget.unavailableCount).toBe(MAX_PAINTED_SKILL_RANGES);
  expect(atBudget.unavailable).toHaveLength(MAX_PAINTED_SKILL_RANGES);
  expect(atBudget.unavailable).toEqual(skillRanges(budget));
  const over = analyzeSkillTokens(budget + "$a ", { names });
  expect(over.unavailableCount).toBe(MAX_PAINTED_SKILL_RANGES + 1);
  expect(over.unavailable).toEqual([]);
});
it(
  "reports the exact count without painting past 256 tokens and restores the aligned overlay at 256",
  { timeout: 60_000 },
  async () => {
    const f = fixture();
    const view = render(f.view());
    const input = screen.getByRole("textbox") as HTMLTextAreaElement;
    const type = (draft: string) =>
      fireEvent.change(input, {
        target: { value: draft, selectionStart: draft.length },
      });
    type("$seed");
    await waitFor(() =>
      expect(
        view.container.querySelectorAll(".skill-token-unavailable"),
      ).toHaveLength(1),
    );
    const large = "$a ".repeat(87_000);
    type(large);
    await waitFor(() =>
      expect(countStatus(view).textContent).toMatch(
        /^87,?000 skill tokens are unavailable in this scope\.$/,
      ),
    );
    expect(view.container.querySelector(".skill-text-overlay")).toBeNull();
    expect(view.container.querySelector(".skill-token-unavailable")).toBeNull();
    expect(input.value).toBe(large);
    expect(countStatus(view).textContent).not.toContain("$a");
    const describedBy = input.getAttribute("aria-describedby")!;
    expect(document.getElementById(describedBy)!.textContent).toMatch(
      /^87,?000 skill tokens are unavailable in this scope\.$/,
    );
    type("$a ".repeat(257));
    await waitFor(() =>
      expect(countStatus(view).textContent).toBe(
        "257 skill tokens are unavailable in this scope.",
      ),
    );
    expect(view.container.querySelector(".skill-text-overlay")).toBeNull();
    const exact = "$a ".repeat(256);
    type(exact);
    await waitFor(() =>
      expect(
        view.container.querySelectorAll(".skill-token-unavailable"),
      ).toHaveLength(256),
    );
    expect(countStatus(view).textContent).toBe(
      "256 skill tokens are unavailable in this scope.",
    );
    expect(
      view.container.querySelector(".skill-text-overlay")!.textContent,
    ).toBe(exact + "\u200b");
    expect(
      view.container.querySelectorAll(".skill-text-overlay span").length,
    ).toBeLessThanOrEqual(512);
  },
);
it("retains only displayed missing rows, skips disabled rows, and discards them on dismissal", async () => {
  let current = entries;
  const send = vi.fn();
  const client = new QueryClient();
  const transport = createRouterTransport((router) =>
    router.service(SkillService, {
      listSkills: async () => ({ skills: current }),
    }),
  );
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <Composer send={send} />
      </QueryClientProvider>
    </TransportProvider>,
  );
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$add", selectionStart: 4 } });
  await screen.findAllByRole("option");
  current = [entries[1]!];
  await client.invalidateQueries();
  const removed = await screen.findByRole("option", {
    name: /add-issue.*Unavailable/,
  });
  expect(removed.getAttribute("aria-disabled")).toBe("true");
  fireEvent.click(removed);
  expect(input).toHaveProperty("value", "$add");
  fireEvent.keyDown(input, { key: "ArrowDown" });
  expect(input.getAttribute("aria-activedescendant")).not.toBe(removed.id);
  fireEvent.keyDown(input, { key: "Tab" });
  expect(input).toHaveProperty("value", "$add-note");
  expect(send).not.toHaveBeenCalled();
  fireEvent.change(input, { target: { value: "$add", selectionStart: 4 } });
  await screen.findByRole("option", { name: /add-note/ });
  expect(screen.queryByRole("option", { name: /add-issue/ })).toBeNull();
  fireEvent.keyDown(input, { key: "Escape" });
  fireEvent.select(input, { target: { selectionStart: 4 } });
  expect(screen.queryByRole("listbox")).toBeNull();
});
it("fences late previous-Runner inventory and leaves pending, failed and unsupported reads unknown", async () => {
  let release!: () => void;
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  const next = newRequestId();
  const client = new QueryClient();
  const transport = createRouterTransport((router) =>
    router.service(SkillService, {
      listSkills: async (request) => {
        if (request.machineId === machine) {
          await pending;
          return { skills: entries };
        }
        throw new Error("Private inventory failure");
      },
    }),
  );
  const tree = (runner: string) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <Composer runner={runner} send={vi.fn()} />
      </QueryClientProvider>
    </TransportProvider>
  );
  const view = render(tree(machine));
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$missing", selectionStart: 8 } });
  await screen.findByText("Loading skills…");
  expect(countStatus(view).textContent).toBe("");
  expect(view.container.querySelector(".skill-text-overlay")).toBeNull();
  view.rerender(tree(next));
  await screen.findByText("Skills are unavailable. Your message is unchanged.");
  release();
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(view.container.querySelector(".skill-text-overlay")).toBeNull();
  expect(screen.queryByRole("option")).toBeNull();
  expect(view.container.textContent).not.toContain("Private inventory failure");
  expect(countStatus(view).textContent).toBe("");
});

it("disabled-only retained rows never accept mouse, Enter or Tab; restored entries bind their fresh selection", async () => {
  let current = [entries[0]!];
  const send = vi.fn();
  const client = new QueryClient();
  const transport = createRouterTransport((router) =>
    router.service(SkillService, {
      listSkills: async () => ({ skills: current }),
    }),
  );
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <Composer send={send} />
      </QueryClientProvider>
    </TransportProvider>,
  );
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$a", selectionStart: 2 } });
  await screen.findByRole("option");
  current = [];
  await client.invalidateQueries();
  const removed = await screen.findByRole("option", { name: /Unavailable/ });
  fireEvent.click(removed);
  // fireEvent returns false when the dispatched event was default-prevented.
  expect(fireEvent.keyDown(input, { key: "Enter" })).toBe(false);
  expect(fireEvent.keyDown(input, { key: "Tab" })).toBe(true);
  expect(fireEvent.keyDown(input, { key: "ArrowDown" })).toBe(false);
  expect(input).toHaveProperty("value", "$a");
  expect(input.getAttribute("aria-activedescendant")).toBeNull();
  expect(send).not.toHaveBeenCalled();
  const fresh = {
    ...entries[0]!,
    selection: {
      ...entries[0]!.selection,
      inventoryId: newRequestId(),
      contentRevision: "b".repeat(64),
    },
  };
  current = [fresh];
  await client.invalidateQueries();
  await waitFor(() =>
    expect(
      screen
        .getAllByRole("option")
        .filter((option) => option.getAttribute("aria-disabled") === null),
    ).toHaveLength(1),
  );
  fireEvent.keyDown(input, { key: "Enter" });
  fireEvent.click(screen.getByText("Send"));
  expect(send).toHaveBeenLastCalledWith({
    value: "$add-issue",
    skills: [fresh.selection],
  });
});
type FixtureEntry = (typeof entries)[number];
// Models the server: every List call mints a new InventoryID and fresh SkillIDs.
function generation(
  list: readonly FixtureEntry[],
  overrides: Partial<FixtureEntry["selection"]> = {},
): FixtureEntry[] {
  const inventoryId = newRequestId();
  return list.map((entry) => ({
    ...entry,
    selection: {
      ...entry.selection,
      inventoryId,
      skillId: newRequestId(),
      ...overrides,
    },
  }));
}
function skillEntry(
  name: string,
  provenance = SkillProvenance.USER,
  contentRevision = "a".repeat(64),
): FixtureEntry {
  return {
    ...entries[0]!,
    name,
    description: `${name} fixture`,
    provenance,
    selection: {
      ...entries[0]!.selection,
      skillId: newRequestId(),
      contentRevision,
    },
  };
}
function mutableFixture(initial: readonly FixtureEntry[]) {
  const state = { current: [...initial], send: vi.fn() };
  const client = new QueryClient();
  const transport = createRouterTransport((router) =>
    router.service(SkillService, {
      listSkills: async () => ({ skills: state.current }),
    }),
  );
  const view = (runner = machine) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <Composer runner={runner} send={state.send} />
      </QueryClientProvider>
    </TransportProvider>
  );
  const refresh = async (next: readonly FixtureEntry[]) => {
    state.current = [...next];
    await client.invalidateQueries();
  };
  return { state, client, view, refresh };
}
function rowStates() {
  return screen.getAllByRole("option").map((option) => ({
    name: option.querySelector("strong")!.textContent,
    unavailable: option.getAttribute("aria-disabled") === "true",
  }));
}
async function expectRows(count: number) {
  await waitFor(() =>
    expect(screen.getAllByRole("option")).toHaveLength(count),
  );
}
it("leaves plain Tab to native focus navigation when only unavailable rows remain", async () => {
  const f = mutableFixture([entries[0]!]);
  render(f.view());
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$a", selectionStart: 2 } });
  await screen.findByRole("option");
  await f.refresh([]);
  await screen.findByRole("option", { name: /Unavailable/ });
  const event = new KeyboardEvent("keydown", {
    key: "Tab",
    bubbles: true,
    cancelable: true,
  });
  const stop = vi.spyOn(event, "stopPropagation");
  act(() => {
    input.dispatchEvent(event);
  });
  expect(event.defaultPrevented).toBe(false);
  expect(stop).not.toHaveBeenCalled();
  expect(input).toHaveProperty("value", "$a");
  expect(screen.getByRole("listbox")).toBeTruthy();
  expect(f.state.send).not.toHaveBeenCalled();
  fireEvent.click(screen.getByText("Send"));
  expect(f.state.send).toHaveBeenLastCalledWith({ value: "$a", skills: [] });
  // Enter stays owned by the open panel and still accepts nothing.
  expect(fireEvent.keyDown(input, { key: "Enter" })).toBe(false);
  expect(input).toHaveProperty("value", "$a");
});
it("still accepts the selected enabled row once with plain Tab and ignores modified Tab", async () => {
  const f = mutableFixture([skillEntry("add-issue"), skillEntry("add-note")]);
  render(f.view());
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$add", selectionStart: 4 } });
  await screen.findAllByRole("option");
  expect(fireEvent.keyDown(input, { key: "Tab", shiftKey: true })).toBe(true);
  expect(fireEvent.keyDown(input, { key: "Tab", ctrlKey: true })).toBe(true);
  expect(fireEvent.keyDown(input, { key: "Tab", metaKey: true })).toBe(true);
  expect(input).toHaveProperty("value", "$add");
  expect(fireEvent.keyDown(input, { key: "Tab" })).toBe(false);
  expect(input).toHaveProperty("value", "$add-issue");
  expect(screen.queryByRole("listbox")).toBeNull();
  // The panel is dismissed after acceptance, so a second Tab is not intercepted.
  expect(fireEvent.keyDown(input, { key: "Tab" })).toBe(true);
  expect(f.state.send).not.toHaveBeenCalled();
});
it("keeps intercepting plain Tab for mixed rows and accepts only the enabled one", async () => {
  const f = mutableFixture([skillEntry("add-issue"), skillEntry("add-note")]);
  render(f.view());
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$add", selectionStart: 4 } });
  await screen.findAllByRole("option");
  await f.refresh([f.state.current[1]!]);
  await screen.findByRole("option", { name: /add-issue.*Unavailable/ });
  expect(fireEvent.keyDown(input, { key: "Tab" })).toBe(false);
  expect(input).toHaveProperty("value", "$add-note");
});
it("does not leave one unavailable duplicate per refresh of unchanged skills", async () => {
  const f = mutableFixture(generation(entries));
  render(f.view());
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$add", selectionStart: 4 } });
  await screen.findAllByRole("option");
  let latest = f.state.current;
  for (let round = 0; round < 3; round += 1) {
    latest = generation(latest);
    await f.refresh(latest);
    await expectRows(2);
    expect(rowStates()).toEqual([
      { name: "add-issue", unavailable: false },
      { name: "add-note", unavailable: false },
    ]);
  }
  // Acceptance binds the latest opaque selection, not an older generation's.
  fireEvent.keyDown(input, { key: "Enter" });
  fireEvent.click(screen.getByText("Send"));
  expect(f.state.send).toHaveBeenLastCalledWith({
    value: "$add-issue",
    skills: [latest[0]!.selection],
  });
});
it("keeps a changed-content row unavailable beside its available replacement until dismissal", async () => {
  const f = mutableFixture(generation(entries));
  render(f.view());
  const input = screen.getByRole("textbox");
  fireEvent.change(input, {
    target: { value: "$add-issue", selectionStart: 10 },
  });
  await screen.findByRole("option");
  const next = generation([entries[0]!], { contentRevision: "b".repeat(64) });
  await f.refresh(next);
  await expectRows(2);
  expect(rowStates()).toEqual([
    { name: "add-issue", unavailable: false },
    { name: "add-issue", unavailable: true },
  ]);
  const [fresh, stale] = screen.getAllByRole("option");
  fireEvent.click(stale!);
  expect(input).toHaveProperty("value", "$add-issue");
  fireEvent.click(fresh!);
  fireEvent.click(screen.getByText("Send"));
  expect(f.state.send).toHaveBeenLastCalledWith({
    value: "$add-issue",
    skills: [next[0]!.selection],
  });
  // Dismissal discards the old-content row; reopening shows only the live row.
  fireEvent.change(input, { target: { value: "$add-i", selectionStart: 6 } });
  await expectRows(1);
  expect(rowStates()).toEqual([{ name: "add-issue", unavailable: false }]);
});
it("matches identical fingerprints by count and keeps differing provenance or content distinct", async () => {
  const dup = skillEntry("dup"),
    twin = {
      ...dup,
      selection: { ...dup.selection, skillId: newRequestId() },
    },
    user = skillEntry("same", SkillProvenance.USER),
    project = skillEntry("same", SkillProvenance.PROJECT),
    revised = skillEntry("same", SkillProvenance.USER, "c".repeat(64));
  const f = mutableFixture(generation([dup, twin, user, project, revised]));
  render(f.view());
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$", selectionStart: 1 } });
  await expectRows(5);
  expect(rowStates().some((row) => row.unavailable)).toBe(false);
  // An unchanged refresh with fresh IDs still has exactly one row per entry.
  await f.refresh(generation(f.state.current));
  await expectRows(5);
  expect(rowStates().some((row) => row.unavailable)).toBe(false);
  // Two indistinguishable duplicates become one: exactly one unavailable row.
  await f.refresh(
    generation(f.state.current.filter((_, index) => index !== 1)),
  );
  await expectRows(5);
  expect(rowStates().filter((row) => row.unavailable)).toEqual([
    { name: "dup", unavailable: true },
  ]);
  // The unavailable row stays single across further unchanged refreshes.
  await f.refresh(generation(f.state.current));
  await expectRows(5);
  expect(rowStates().filter((row) => row.unavailable)).toHaveLength(1);
  // Dropping one provenance variant retains only that variant as unavailable.
  await f.refresh(
    generation(
      f.state.current.filter(
        (entry) => entry.provenance !== SkillProvenance.PROJECT,
      ),
    ),
  );
  await expectRows(5);
  expect(rowStates().filter((row) => row.unavailable)).toHaveLength(2);
});
it("caps retained rows at 256 with live rows first and clears them on scope replacement", async () => {
  const old = Array.from({ length: 200 }, (_, index) =>
    skillEntry(`old-${index}`),
  );
  const f = mutableFixture(generation(old));
  const view = render(f.view());
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$", selectionStart: 1 } });
  await expectRows(200);
  await f.refresh(
    generation(
      Array.from({ length: 100 }, (_, index) => skillEntry(`new-${index}`)),
    ),
  );
  await expectRows(256);
  const states = rowStates();
  expect(states.filter((row) => !row.unavailable)).toHaveLength(100);
  expect(states.filter((row) => row.unavailable)).toHaveLength(156);
  // Scope replacement retires every retained unavailable row.
  view.rerender(f.view(newRequestId()));
  await expectRows(100);
  expect(rowStates().some((row) => row.unavailable)).toBe(false);
});
it("observes history-like tokens while dismissed, and does not claim absence from incomplete inventory", async () => {
  let current = [
    entries[0]!,
    {
      ...entries[1]!,
      selection: { ...entries[1]!.selection, inventoryId: newRequestId() },
    },
  ];
  const client = new QueryClient();
  const transport = createRouterTransport((router) =>
    router.service(SkillService, {
      listSkills: async () => ({ skills: current }),
    }),
  );
  const view = render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <Composer send={vi.fn()} />
      </QueryClientProvider>
    </TransportProvider>,
  );
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$removed", selectionStart: 8 } });
  await screen.findByText("Skills are unavailable. Your message is unchanged.");
  expect(countStatus(view).textContent).toBe("");
  expect(view.container.querySelector(".skill-text-overlay")).toBeNull();
  fireEvent.keyDown(input, { key: "Escape" });
  current = [entries[0]!];
  await client.invalidateQueries();
  await waitFor(() =>
    expect(
      view.container.querySelector(".skill-token-unavailable")?.textContent,
    ).toBe("$removed"),
  );
  expect(screen.queryByRole("listbox")).toBeNull();
});

it("leaves unsupported inventory unknown without reads and fences replacement transports even when binding context is retained", async () => {
  let release!: () => void;
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  const oldRead = vi.fn(async () => {
    await pending;
    return {
      skills: [
        ...entries,
        {
          ...entries[0]!,
          name: "missing",
          selection: { ...entries[0]!.selection, skillId: newRequestId() },
        },
      ],
    };
  });
  const oldTransport = createRouterTransport((router) =>
    router.service(SkillService, { listSkills: oldRead }),
  );
  const freshTransport = createRouterTransport((router) =>
    router.service(SkillService, {
      listSkills: async () => ({ skills: entries }),
    }),
  );
  const client = new QueryClient();
  const tree = (transport: typeof oldTransport, enabled = true) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <Composer enabled={enabled} retainTransportContext send={vi.fn()} />
      </QueryClientProvider>
    </TransportProvider>
  );
  const view = render(tree(oldTransport, false));
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$missing", selectionStart: 8 } });
  await screen.findByText("Update the server and Runner Device to use skills.");
  expect(oldRead).not.toHaveBeenCalled();
  expect(view.container.querySelector(".skill-text-overlay")).toBeNull();
  view.rerender(tree(oldTransport));
  await waitFor(() => expect(oldRead).toHaveBeenCalledOnce());
  view.rerender(tree(freshTransport));
  await waitFor(() =>
    expect(
      view.container.querySelector(".skill-token-unavailable")?.textContent,
    ).toBe("$missing"),
  );
  await waitFor(() =>
    expect(countStatus(view).textContent).toBe(
      "1 skill token is unavailable in this scope.",
    ),
  );
  const status = countStatus(view),
    announcement = status.firstChild;
  // The old request has a different inventory that would falsely make this name available.
  release();
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(
    view.container.querySelector(".skill-token-unavailable")?.textContent,
  ).toBe("$missing");
  expect(status.firstChild).toBe(announcement);
});

function RecalledSkillComposer() {
  const [value, change] = useState("$add-issue"),
    textarea = useRef<HTMLTextAreaElement>(null);
  const skills = useSkillCompletion({
    value,
    change,
    textarea,
    machineId: machine,
    agentId: agent,
    initialBindings: [
      {
        start: 0,
        end: 10,
        token: "$add-issue",
        selection: entries[0]!.selection,
        stale: false,
      },
    ],
  });
  return (
    <>
      {skills.wrap(
        <textarea
          aria-label="Recall fixture"
          ref={textarea}
          value={value}
          onChange={(event) =>
            skills.onChange(event.target.value, event.target.selectionStart)
          }
          {...skills.attributes}
        />,
      )}
      {skills.list}
      <button
        onClick={() => skills.replaceUnbound("$add-issue $removed-skill", 0)}
      >
        Recall missing text
      </button>
      <output data-recalled-selections>{skills.selections.length}</output>
    </>
  );
}
it("restores missing history-like tokens as exact text with no candidates or binding authority", async () => {
  const transport = createRouterTransport((router) =>
    router.service(SkillService, {
      listSkills: async () => ({ skills: entries }),
    }),
  );
  const view = render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={new QueryClient()}>
        <RecalledSkillComposer />
      </QueryClientProvider>
    </TransportProvider>,
  );
  fireEvent.click(screen.getByText("Recall missing text"));
  await waitFor(() =>
    expect(
      view.container.querySelector(".skill-token-unavailable")?.textContent,
    ).toBe("$removed-skill"),
  );
  expect(screen.getByRole("textbox")).toHaveProperty(
    "value",
    "$add-issue $removed-skill",
  );
  expect(
    view.container.querySelector("[data-recalled-selections]")?.textContent,
  ).toBe("0");
  expect(screen.queryByRole("listbox")).toBeNull();
});
it("prioritizes a complete live inventory at the retention bound instead of carrying displaced rows", async () => {
  let current = entries;
  const client = new QueryClient();
  const transport = createRouterTransport((router) =>
    router.service(SkillService, {
      listSkills: async () => ({ skills: current }),
    }),
  );
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <Composer send={vi.fn()} />
      </QueryClientProvider>
    </TransportProvider>,
  );
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$", selectionStart: 1 } });
  await screen.findAllByRole("option");
  current = Array.from({ length: 256 }, (_, index) => ({
    ...entries[0]!,
    name: `live-${index}`,
    selection: { ...entries[0]!.selection, skillId: newRequestId() },
  }));
  await client.invalidateQueries();
  await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(256));
  expect(screen.queryByText("add-issue", { selector: "strong" })).toBeNull();
  expect(screen.queryByRole("option", { name: /Unavailable/ })).toBeNull();
});

it("announces a deferred dismissed-panel count without moving focus and dedupes unchanged results", async () => {
  const f = fixture();
  let release!: () => void;
  f.read.mockImplementationOnce(async () => {
    await new Promise<void>((resolve) => {
      release = resolve;
    });
    return { skills: entries };
  });
  const view = render(f.view());
  const status = countStatus(view);
  expect(status.getAttribute("aria-live")).toBe("polite");
  expect(status.getAttribute("aria-atomic")).toBe("true");
  expect(status.hasAttribute("aria-hidden")).toBe(false);
  expect(status.textContent).toBe("");
  const input = screen.getByRole("textbox");
  input.focus();
  fireEvent.change(input, {
    target: { value: "$missing ", selectionStart: 9 },
  });
  fireEvent.keyDown(input, { key: "Escape" });
  await waitFor(() => expect(f.read).toHaveBeenCalledOnce());
  await act(async () => release());
  await waitFor(() =>
    expect(status.textContent).toBe(
      "1 skill token is unavailable in this scope.",
    ),
  );
  expect(countStatus(view)).toBe(status);
  expect(document.activeElement).toBe(input);
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(
    screen.getByRole("textbox", {
      description: "1 skill token is unavailable in this scope.",
    }),
  ).toBe(input);
  const text = status.firstChild;
  fireEvent.change(input, {
    target: { value: "ordinary $missing ", selectionStart: 18 },
  });
  await act(async () => {});
  expect(status.firstChild).toBe(text);
  expect(document.activeElement).toBe(input);
  // Count only added nonempty announcements; clearing an unknown observation
  // cannot announce that every token has become available.
  const updates: string[] = [];
  const observer = new MutationObserver(() => {
    if (status.textContent) updates.push(status.textContent);
  });
  observer.observe(status, {
    childList: true,
    characterData: true,
    subtree: true,
  });
  try {
    await act(async () => {
      await f.client.invalidateQueries();
    });
    expect(f.read).toHaveBeenCalledTimes(2);
    expect(updates).toEqual([]);
    expect(countStatus(view)).toBe(status);
    expect(f.send).not.toHaveBeenCalled();
  } finally {
    observer.disconnect();
  }
});

it.each([
  [
    SupportedLanguage.English,
    "1 skill token is unavailable in this scope.",
    "No skill tokens are unavailable in this scope.",
  ],
  [
    SupportedLanguage.Korean,
    "현재 범위에서 스킬 토큰 1개를 사용할 수 없습니다.",
    "현재 범위에서 사용할 수 없는 스킬 토큰이 없습니다.",
  ],
])(
  "localizes confirmed recovery without announcing loading as zero in %s",
  async (language, absent, recovered) => {
    await i18n.changeLanguage(language);
    let current = entries;
    let release!: () => void;
    let pending: Promise<void> | undefined;
    const client = new QueryClient();
    const transport = createRouterTransport((router) =>
      router.service(SkillService, {
        listSkills: async () => {
          await pending;
          return { skills: current };
        },
      }),
    );
    const view = render(
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <Composer send={vi.fn()} />
        </QueryClientProvider>
      </TransportProvider>,
    );
    try {
      const input = screen.getByRole("textbox");
      input.focus();
      fireEvent.change(input, {
        target: { value: "$missing ", selectionStart: 9 },
      });
      const status = countStatus(view);
      await waitFor(() => expect(status.textContent).toBe(absent));
      pending = new Promise<void>((resolve) => {
        release = resolve;
      });
      let refetch!: Promise<void>;
      act(() => {
        refetch = client.invalidateQueries();
      });
      await waitFor(() => expect(status.textContent).toBe(""));
      await act(async () => {
        release();
        await refetch;
      });
      expect(
        status.textContent,
        "an unchanged confirmed count is not replayed",
      ).toBe("");
      pending = undefined;
      current = [
        ...entries,
        {
          ...entries[0]!,
          name: "missing",
          selection: { ...entries[0]!.selection, skillId: newRequestId() },
        },
      ];
      await act(async () => {
        await client.invalidateQueries();
      });
      await waitFor(() => expect(status.textContent).toBe(recovered));
      expect(document.activeElement).toBe(input);
      expect((input as HTMLTextAreaElement).value).toBe("$missing ");
      expect(input.hasAttribute("aria-describedby")).toBe(false);
    } finally {
      view.unmount();
      await i18n.changeLanguage(SupportedLanguage.English);
    }
  },
);

it("retires a historical live announcement instead of translating it on locale change", async () => {
  await i18n.changeLanguage(SupportedLanguage.English);
  const f = fixture();
  const view = render(f.view());
  const input = screen.getByRole("textbox"),
    status = countStatus(view);
  fireEvent.change(input, {
    target: { value: "$missing ", selectionStart: 9 },
  });
  await waitFor(() =>
    expect(status.textContent).toBe(
      "1 skill token is unavailable in this scope.",
    ),
  );
  const announcements: string[] = [];
  const observer = new MutationObserver(() => {
    if (status.textContent) announcements.push(status.textContent);
  });
  observer.observe(status, {
    childList: true,
    characterData: true,
    subtree: true,
  });
  try {
    await act(async () => {
      await i18n.changeLanguage(SupportedLanguage.Korean);
    });
    await act(async () => {});
    expect(status.textContent).toBe("");
    expect(announcements).toEqual([]);
    fireEvent.change(input, {
      target: { value: "$add-issue ", selectionStart: 11 },
    });
    await waitFor(() =>
      expect(status.textContent).toBe(
        "현재 범위에서 사용할 수 없는 스킬 토큰이 없습니다.",
      ),
    );
  } finally {
    observer.disconnect();
    view.unmount();
    await i18n.changeLanguage(SupportedLanguage.English);
  }
});

it("defers confirmed count publication during composition and suppresses locked outcomes", async () => {
  const f = fixture();
  let release!: () => void;
  f.read.mockImplementationOnce(async () => {
    await new Promise<void>((resolve) => {
      release = resolve;
    });
    return { skills: entries };
  });
  const view = render(f.view());
  const input = screen.getByRole("textbox");
  input.focus();
  fireEvent.change(input, {
    target: { value: "$missing ", selectionStart: 9 },
  });
  await waitFor(() => expect(f.read).toHaveBeenCalledOnce());
  fireEvent.compositionStart(input);
  await act(async () => release());
  await act(async () => {});
  expect(countStatus(view).textContent).toBe("");
  fireEvent.compositionEnd(input);
  await waitFor(() =>
    expect(countStatus(view).textContent).toBe(
      "1 skill token is unavailable in this scope.",
    ),
  );
  view.rerender(f.view(machine, true));
  expect(countStatus(view).textContent).toBe("");
  expect(f.read).toHaveBeenCalledOnce();
  expect((input as HTMLTextAreaElement).value).toBe("$missing ");
  expect(f.send).not.toHaveBeenCalled();
});

it.each(["mouse", "Enter", "Tab"])(
  "paints only the explicit %s binding while retaining Korean bytes, focus and sole textarea ownership",
  async (method) => {
    const f = fixture();
    const view = render(f.view());
    const input = screen.getByRole("textbox") as HTMLTextAreaElement;
    input.focus();
    fireEvent.change(input, {
      target: { value: "한글\n$add-iss", selectionStart: 11 },
    });
    const option = await screen.findByRole("option");
    if (method === "mouse") {
      fireEvent.mouseDown(option);
      fireEvent.click(option);
    } else fireEvent.keyDown(input, { key: method });
    await waitFor(() => expect(input.selectionStart).toBe(13));
    const painted = view.container.querySelector(".skill-token-selected")!;
    expect(painted.textContent).toBe("$add-issue");
    expect(painted.getAttribute("data-skill-start")).toBe("3");
    expect(input.value).toBe("한글\n$add-issue");
    expect(document.activeElement).toBe(input);
    expect(view.container.querySelectorAll(".skill-text-overlay")).toHaveLength(
      1,
    );
    expect(painted.closest("[aria-hidden=true]")).toBeTruthy();
    expect(painted.closest("a,[role=link],[tabindex]")).toBeNull();
    expect(input.getAttribute("aria-describedby")).toBeNull();
    expect(f.send).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("Send"));
    expect(f.send).toHaveBeenLastCalledWith({
      value: "한글\n$add-issue",
      skills: [entries[0]!.selection],
    });
    fireEvent.change(input, {
      target: { value: "Before 한글\n$add-issue", selectionStart: 20 },
    });
    expect(
      view.container
        .querySelector(".skill-token-selected")
        ?.getAttribute("data-skill-start"),
    ).toBe("10");
    fireEvent.change(input, {
      target: { value: "Before 한글\n$add-note", selectionStart: 19 },
    });
    expect(view.container.querySelector(".skill-token-selected")).toBeNull();
  },
);

it("keeps manually typed matching tokens plain and removes selected decoration on unresolved or changed scope", async () => {
  const f = fixture();
  const view = render(f.view());
  const input = screen.getByRole("textbox");
  fireEvent.change(input, {
    target: { value: "$add-issue", selectionStart: 10 },
  });
  await screen.findByRole("option");
  expect(view.container.querySelector(".skill-token-selected")).toBeNull();
  fireEvent.keyDown(input, { key: "Tab" });
  expect(view.container.querySelectorAll(".skill-token-selected")).toHaveLength(
    1,
  );
  view.rerender(f.view(""));
  expect(view.container.querySelector(".skill-token-selected")).toBeNull();
  expect(screen.getByText("Send")).toHaveProperty("disabled", true);
  view.rerender(f.view(newRequestId()));
  await screen.findByText(
    "The selected skills need reselection before sending.",
  );
  expect(view.container.querySelector(".skill-token-selected")).toBeNull();
  expect(input).toHaveProperty("value", "$add-issue");
});

it("retains selected decoration through locks and unknown inventory, but confirmed absence wins and IME suppresses it", async () => {
  const f = fixture();
  const view = render(f.view());
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$add-iss", selectionStart: 8 } });
  await screen.findByRole("option");
  fireEvent.keyDown(input, { key: "Enter" });
  expect(view.container.querySelectorAll(".skill-token-selected")).toHaveLength(
    1,
  );
  fireEvent.compositionStart(input);
  expect(view.container.querySelector(".skill-text-overlay")).toBeNull();
  fireEvent.compositionEnd(input);
  expect(view.container.querySelectorAll(".skill-token-selected")).toHaveLength(
    1,
  );
  view.rerender(f.view(machine, true));
  expect(view.container.querySelectorAll(".skill-token-selected")).toHaveLength(
    1,
  );
  fireEvent.change(input, {
    target: { value: "locked replacement", selectionStart: 18 },
  });
  expect(input).toHaveProperty("value", "$add-issue");
  expect(screen.queryByRole("option")).toBeNull();
  view.rerender(f.view());
  await waitFor(() => expect(f.client.isFetching()).toBe(0));
  let release!: () => void;
  f.read.mockImplementationOnce(async () => {
    await new Promise<void>((resolve) => {
      release = resolve;
    });
    return { skills: entries };
  });
  const pending = f.client.invalidateQueries();
  await waitFor(() => expect(release).toBeDefined());
  expect(view.container.querySelectorAll(".skill-token-selected")).toHaveLength(
    1,
  );
  expect(view.container.querySelector(".skill-token-unavailable")).toBeNull();
  release();
  await pending;
  f.read.mockRejectedValueOnce(new Error("Synthetic inventory failure"));
  await f.client.invalidateQueries();
  expect(view.container.querySelectorAll(".skill-token-selected")).toHaveLength(
    1,
  );
  expect(view.container.querySelector(".skill-token-unavailable")).toBeNull();
  f.read.mockResolvedValueOnce({ skills: [] });
  await f.client.invalidateQueries();
  await waitFor(() =>
    expect(
      view.container.querySelectorAll(".skill-token-unavailable"),
    ).toHaveLength(1),
  );
  expect(view.container.querySelector(".skill-token-selected")).toBeNull();
  fireEvent.compositionStart(input);
  expect(view.container.querySelector(".skill-text-overlay")).toBeNull();
  fireEvent.compositionEnd(input);
  await waitFor(() =>
    expect(view.container.querySelector(".skill-text-overlay")).not.toBeNull(),
  );
  expect(input).toHaveProperty("value", "$add-issue");
  expect(f.send).not.toHaveBeenCalled();
});

type SelectionField =
  "workerDeviceId" | "inventoryId" | "skillId" | "contentRevision";
function replaceSelection(field: SelectionField) {
  // Keep the snapshot internally consistent while changing exactly one field.
  const value = field === "contentRevision" ? "b".repeat(64) : newRequestId();
  return entries.map((entry, index) => ({
    ...entry,
    selection: {
      ...entry.selection,
      ...(field === "skillId"
        ? index === 0
          ? { skillId: value }
          : {}
        : field === "contentRevision"
          ? index === 0
            ? { contentRevision: value }
            : {}
          : { [field]: value }),
    },
  }));
}
it.each([
  "workerDeviceId",
  "inventoryId",
  "skillId",
  "contentRevision",
] as const)(
  "treats a bound token as unavailable when only its %s is absent from a same-named inventory entry",
  async (field) => {
    const f = fixture();
    const view = render(f.view());
    const input = screen.getByRole("textbox") as HTMLTextAreaElement;
    fireEvent.change(input, {
      target: { value: "$add-iss $gone", selectionStart: 8 },
    });
    await screen.findByRole("option");
    fireEvent.keyDown(input, { key: "Enter" });
    expect(input.value).toBe("$add-issue $gone");
    await waitFor(() =>
      expect(
        [...view.container.querySelectorAll(".skill-token-unavailable")].map(
          (node) => node.textContent,
        ),
      ).toEqual(["$gone"]),
    );
    expect(
      [...view.container.querySelectorAll(".skill-token-selected")].map(
        (node) => node.textContent,
      ),
    ).toEqual(["$add-issue"]);
    // The same four fields keep the selected decoration after a refresh.
    await act(async () => {
      await f.client.invalidateQueries();
    });
    await waitFor(() => expect(f.client.isFetching()).toBe(0));
    expect(
      view.container.querySelectorAll(".skill-token-selected"),
    ).toHaveLength(1);
    const replaced = replaceSelection(field);
    f.read.mockImplementation(async () => ({ skills: replaced }));
    await act(async () => {
      await f.client.invalidateQueries();
    });
    await waitFor(() =>
      expect(
        [...view.container.querySelectorAll(".skill-token-unavailable")].map(
          (node) => node.textContent,
        ),
      ).toEqual(["$add-issue", "$gone"]),
    );
    expect(view.container.querySelector(".skill-token-selected")).toBeNull();
    expect(countStatus(view).textContent).toBe(
      "2 skill tokens are unavailable in this scope.",
    );
    expect(input.getAttribute("aria-describedby")).toBeTruthy();
    expect(input.value).toBe("$add-issue $gone");
    // Explicit reselection binds the new current selection without moving text or focus.
    input.focus();
    fireEvent.change(input, {
      target: { value: "$add-issu $gone", selectionStart: 9 },
    });
    await screen.findByRole("option", { name: /add-issue/ });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(input.value).toBe("$add-issue $gone");
    await waitFor(() =>
      expect(
        [...view.container.querySelectorAll(".skill-token-selected")].map(
          (node) => node.textContent,
        ),
      ).toEqual(["$add-issue"]),
    );
    expect(
      [...view.container.querySelectorAll(".skill-token-unavailable")].map(
        (node) => node.textContent,
      ),
    ).toEqual(["$gone"]);
    await act(async () => {});
    expect(document.activeElement).toBe(input);
    expect(input.selectionStart).toBe(10);
    fireEvent.click(screen.getByText("Send"));
    expect(f.send).toHaveBeenLastCalledWith({
      value: "$add-issue $gone",
      skills: [replaced[0]!.selection],
    });
  },
);
it("keeps a bound token confirmed unavailable when its display name changes and preserves unknown or IME behavior", async () => {
  const f = fixture();
  const view = render(f.view());
  const input = screen.getByRole("textbox") as HTMLTextAreaElement;
  fireEvent.change(input, { target: { value: "$add-iss", selectionStart: 8 } });
  await screen.findByRole("option");
  fireEvent.keyDown(input, { key: "Enter" });
  await waitFor(() =>
    expect(
      view.container.querySelectorAll(".skill-token-selected"),
    ).toHaveLength(1),
  );
  // Unknown reads fabricate no absence and keep the selected decoration.
  f.read.mockRejectedValueOnce(new Error("Synthetic inventory failure"));
  await act(async () => {
    await f.client.invalidateQueries();
  });
  expect(view.container.querySelectorAll(".skill-token-selected")).toHaveLength(
    1,
  );
  expect(view.container.querySelector(".skill-token-unavailable")).toBeNull();
  // Same selection, new display name: the token name is absent and the selection is not enough.
  f.read.mockImplementation(async () => ({
    skills: [{ ...entries[0]!, name: "renamed-skill" }],
  }));
  await act(async () => {
    await f.client.invalidateQueries();
  });
  await waitFor(() =>
    expect(
      view.container.querySelectorAll(".skill-token-unavailable"),
    ).toHaveLength(1),
  );
  expect(view.container.querySelector(".skill-token-selected")).toBeNull();
  expect(countStatus(view).textContent).toBe(
    "1 skill token is unavailable in this scope.",
  );
  fireEvent.compositionStart(input);
  expect(view.container.querySelector(".skill-text-overlay")).toBeNull();
  fireEvent.compositionEnd(input);
  await waitFor(() =>
    expect(
      view.container.querySelectorAll(".skill-token-unavailable"),
    ).toHaveLength(1),
  );
  expect(input.value).toBe("$add-issue");
});
function BoundDecorationComposer({
  prompt,
  initialBindings,
}: {
  prompt: string;
  initialBindings: SkillTokenBinding[];
}) {
  const [value, change] = useState(prompt);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const skills = useSkillCompletion({
    value,
    change,
    textarea,
    machineId: machine,
    agentId: agent,
    enabled: false,
    initialBindings,
  });
  return skills.wrap(
    <textarea
      aria-label="Retained selected draft"
      ref={textarea}
      value={value}
      readOnly
    />,
  );
}
it("keeps the first 16 selected decorations when more distinct skills are bound", () => {
  const tokens = Array.from({ length: 17 }, (_, index) => `$skill-${index}`),
    prompt = tokens.join(" "),
    bindings = skillRanges(prompt).map((range) => ({
      start: range.start,
      end: range.end,
      token: `$${range.prefix}`,
      selection: { ...entries[0]!.selection, skillId: newRequestId() },
      stale: false,
      context: `${machine}:${agent}::`,
    }));
  const f = fixture();
  const view = render(
    <TransportProvider
      transport={createRouterTransport((router) =>
        router.service(SkillService, { listSkills: f.read }),
      )}
    >
      <QueryClientProvider client={f.client}>
        <BoundDecorationComposer prompt={prompt} initialBindings={bindings} />
      </QueryClientProvider>
    </TransportProvider>,
  );

  const selected = view.container.querySelectorAll(".skill-token-selected");
  expect([...selected].map((span) => span.textContent)).toEqual(
    tokens.slice(0, 16),
  );
  expect(f.read).not.toHaveBeenCalled();
});
it("bounds painted explicit spans and leaves duplicate, stale or ambiguous binding claims plain", () => {
  const prompt = Array.from({ length: 2048 }, () => "$add-issue").join(" ");
  const bindings = skillRanges(prompt).map((range) => ({
    start: range.start,
    end: range.end,
    token: "$add-issue",
    selection: entries[0]!.selection,
    stale: false,
    context: `${machine}:${agent}::`,
  }));
  const f = fixture();
  const tree = (initialBindings: SkillTokenBinding[]) => (
    <TransportProvider
      transport={createRouterTransport((router) =>
        router.service(SkillService, { listSkills: f.read }),
      )}
    >
      <QueryClientProvider client={f.client}>
        <BoundDecorationComposer
          prompt={prompt}
          initialBindings={initialBindings}
        />
      </QueryClientProvider>
    </TransportProvider>
  );
  const view = render(tree(bindings));
  expect(view.container.querySelectorAll(".skill-token-selected")).toHaveLength(
    16,
  );
  // A new authenticated connection is a scope fence, even with reads disabled.
  view.rerender(tree(bindings));
  expect(view.container.querySelector(".skill-token-selected")).toBeNull();
  view.unmount();
  const duplicate = { ...bindings[0]! };
  const invalid = render(
    tree([
      bindings[0]!,
      duplicate,
      { ...bindings[1]!, stale: true },
      { ...bindings[2]!, ambiguous: true },
      {
        ...bindings[3]!,
        selection: { ...entries[0]!.selection, contentRevision: "invalid" },
      },
    ]),
  );
  expect(invalid.container.querySelector(".skill-token-selected")).toBeNull();
  expect(f.read).not.toHaveBeenCalled();
});
it("realigns mirror typography when the composer appearance changes, including notifications from another window", async () => {
  const style = document.createElement("style");
  style.textContent = `
    textarea { font-size: 12px; line-height: 18px; }
    [data-composer-size="14"] textarea { font-size: 14px; line-height: 21px; }
    [data-composer-size="18"] textarea { font-size: 18px; line-height: 27px; }
  `;
  document.head.append(style);
  let snapshot: AppearanceSnapshot = {
    revision: 1,
    theme: Theme.Light,
    problem: null,
    preferences: defaultPreferences(),
  };
  const listeners = new Set<(value: unknown) => void>();
  const bridge: AppearanceBridge = {
    read: vi.fn(async () => snapshot),
    update: vi.fn(async () => snapshot),
    subscribe: vi.fn(async (changed) => {
      listeners.add(changed);
      return () => {
        listeners.delete(changed);
      };
    }),
  };
  const publish = (next: AppearanceSnapshot) =>
    act(() => {
      snapshot = next;
      listeners.forEach((changed) => changed(next));
    });
  try {
    const f = fixture();
    const view = render(
      <AppearanceProvider bridge={bridge}>{f.view()}</AppearanceProvider>,
    );
    const input = screen.getByRole("textbox") as HTMLTextAreaElement;
    fireEvent.change(input, {
      target: { value: "keep $draft", selectionStart: 11 },
    });
    await waitFor(() =>
      expect(
        view.container.querySelectorAll(".skill-token-unavailable"),
      ).toHaveLength(1),
    );
    await waitFor(() => expect(bridge.subscribe).toHaveBeenCalled());
    await waitFor(() =>
      expect(document.documentElement.dataset.composerSize).toBe("0"),
    );
    input.setSelectionRange(2, 8);
    const mirror = () =>
      view.container.querySelector<HTMLElement>(".skill-text-overlay > div")!;
    expect(mirror().style.fontSize).toBe("12px");
    expect(mirror().style.lineHeight).toBe("18px");
    const spy = vi.spyOn(window, "getComputedStyle");
    // Another window changes only the composer size.
    publish({
      ...snapshot,
      revision: 2,
      preferences: { ...defaultPreferences(), composer_size: 14 },
    });
    await waitFor(() => expect(mirror().style.fontSize).toBe("14px"));
    expect(mirror().style.lineHeight).toBe("21px");
    expect(screen.getByRole("textbox")).toBe(input);
    expect(input.value).toBe("keep $draft");
    expect([input.selectionStart, input.selectionEnd]).toEqual([2, 8]);
    // A change that leaves the composer typography unchanged does not realign.
    spy.mockClear();
    publish({ ...snapshot, revision: 3, theme: Theme.Dark });
    await waitFor(() =>
      expect(document.documentElement.dataset.theme).toBe(Theme.Dark),
    );
    expect(spy).not.toHaveBeenCalled();
    publish({
      ...snapshot,
      revision: 4,
      preferences: { ...defaultPreferences(), composer_size: 18 },
    });
    await waitFor(() => expect(mirror().style.fontSize).toBe("18px"));
    expect(mirror().style.lineHeight).toBe("27px");
    expect(input.value).toBe("keep $draft");
    spy.mockRestore();
  } finally {
    style.remove();
    delete document.documentElement.dataset.composerSize;
    delete document.documentElement.dataset.theme;
  }
});
