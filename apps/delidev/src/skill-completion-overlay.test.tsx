// SPDX-License-Identifier: Apache-2.0
import { useRef, useState } from "react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  SkillService,
  SkillProvenance,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { useSkillCompletion } from "./skill-completion";
import { skillCompletionGeometry } from "./skill-completion-overlay";
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
it("prefers above, falls below only when it fits, and constrains the larger side", () => {
  const bounds = { left: 20, top: 100, right: 380, bottom: 600 };
  expect(
    skillCompletionGeometry(
      { left: 40, top: 350, right: 240, bottom: 390, width: 200 },
      bounds,
      200,
    ),
  ).toEqual({ left: 40, top: 142, width: 200, maxHeight: 220 });
  expect(
    skillCompletionGeometry(
      { left: 40, top: 150, right: 240, bottom: 190, width: 200 },
      bounds,
      200,
    ),
  ).toEqual({ left: 40, top: 198, width: 200, maxHeight: 220 });
  expect(
    skillCompletionGeometry(
      { left: 300, top: 250, right: 620, bottom: 290, width: 320 },
      { left: 0, top: 0, right: 340, bottom: 400 },
      500,
    ),
  ).toEqual({ left: 12, top: 22, width: 320, maxHeight: 220 });
  expect(
    skillCompletionGeometry(
      { left: 80, top: 70, right: 400, bottom: 110, width: 320 },
      { left: 0, top: 0, right: 180, bottom: 140 },
      280,
    ),
  ).toEqual({ left: 8, top: 8, width: 164, maxHeight: 54 });
});
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
function fixture() {
  const send = vi.fn(),
    read = vi.fn(async () => ({ skills: entries }));
  const transport = createRouterTransport((router) =>
    router.service(SkillService, { listSkills: read }),
  );
  const client = new QueryClient();
  const view = (runner = machine, locked = false, enabled = true) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <Composer
          runner={runner}
          locked={locked}
          enabled={enabled}
          send={send}
        />
      </QueryClientProvider>
    </TransportProvider>
  );
  return { send, read, view, client };
}
it("shares the manual overlay through loading, failed read and explicit retry", async () => {
  const f = fixture();
  f.read.mockRejectedValueOnce(new Error("Synthetic unavailable inventory"));
  const view = render(f.view()),
    input = screen.getByRole("textbox");
  input.focus();
  fireEvent.change(input, { target: { value: "$", selectionStart: 1 } });
  const popup = view.container.querySelector<HTMLElement>(".skill-completion")!;
  expect(popup.getAttribute("popover")).toBe("manual");
  expect(popup.querySelector("[role=status]")).not.toBeNull();
  fireEvent.click(await screen.findByRole("button", { name: "Retry" }));
  await screen.findAllByRole("option");
  expect(view.container.querySelector(".skill-completion")).toBe(popup);
  expect(document.activeElement).toBe(input);
  expect(f.send).not.toHaveBeenCalled();
});
it("uses the same manual overlay for unsupported scope without reading or selecting", () => {
  const f = fixture(),
    view = render(f.view(machine, false, false));
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$", selectionStart: 1 } });
  expect(
    view.container.querySelector(".skill-completion")?.getAttribute("popover"),
  ).toBe("manual");
  expect(f.read).not.toHaveBeenCalled();
  expect(screen.queryByRole("option")).toBeNull();
  fireEvent.keyDown(input, { key: "Escape" });
  expect(view.container.querySelector(".skill-completion")).toBeNull();
});
it("retains one manual popover and textarea focus through scrolling and result changes", async () => {
  const f = fixture(),
    view = render(f.view());
  const input = screen.getByRole("textbox");
  input.focus();
  fireEvent.change(input, { target: { value: "$", selectionStart: 1 } });
  await screen.findAllByRole("option");
  const popup = view.container.querySelector<HTMLElement>(".skill-completion")!;
  expect(popup.getAttribute("popover")).toBe("manual");
  fireEvent.resize(window);
  fireEvent.scroll(document);
  fireEvent.change(input, { target: { value: "$add-iss", selectionStart: 8 } });
  await screen.findByRole("option");
  expect(view.container.querySelector(".skill-completion")).toBe(popup);
  fireEvent.change(input, {
    target: { value: "$no-match", selectionStart: 9 },
  });
  expect(view.container.querySelector(".skill-completion")).toBe(popup);
  expect(screen.queryByRole("option")).toBeNull();
  expect(document.activeElement).toBe(input);
  expect(f.send).not.toHaveBeenCalled();
  fireEvent.keyDown(input, { key: "Escape" });
  expect(view.container.querySelector(".skill-completion")).toBeNull();
});
it.each(["hidden", "inert", "disabled", "style"])(
  "disposes completion when the original composer gains %s and never accepts hidden input",
  async (attribute) => {
    const f = fixture(),
      view = render(f.view());
    const input = screen.getByRole("textbox");
    fireEvent.change(input, { target: { value: "$", selectionStart: 1 } });
    const options = await screen.findAllByRole("option");
    const owner = view.container.querySelector("fieldset")!;
    owner.setAttribute(attribute, attribute === "style" ? "display:none" : "");
    fireEvent.click(options[0]!);
    expect((input as HTMLTextAreaElement).value).toBe("$");
    await waitFor(() =>
      expect(view.container.querySelector(".skill-completion")).toBeNull(),
    );
    expect(f.send).not.toHaveBeenCalled();
  },
);

it("keeps the original modal owner and dismisses when that dialog closes", async () => {
  const f = fixture(),
    view = render(<dialog open>{f.view()}</dialog>);
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$", selectionStart: 1 } });
  const options = await screen.findAllByRole("option"),
    dialog = view.container.querySelector("dialog")!,
    popup = view.container.querySelector(".skill-completion")!;
  expect(popup.closest("dialog")).toBe(dialog);
  dialog.removeAttribute("open");
  fireEvent.click(options[0]!);
  expect((input as HTMLTextAreaElement).value).toBe("$");
  await waitFor(() =>
    expect(view.container.querySelector(".skill-completion")).toBeNull(),
  );
  expect(f.send).not.toHaveBeenCalled();
});

it("disposes a disconnected anchor and removes position listeners on unmount", async () => {
  const f = fixture(),
    view = render(f.view());
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "$", selectionStart: 1 } });
  await screen.findAllByRole("option");
  const remove = vi.spyOn(document, "removeEventListener");
  input.remove();
  await waitFor(() =>
    expect(view.container.querySelector(".skill-completion")).toBeNull(),
  );
  expect(remove).toHaveBeenCalledWith("scroll", expect.any(Function), true);
  view.unmount();
  fireEvent.resize(window);
  fireEvent.scroll(document);
  expect(f.send).not.toHaveBeenCalled();
});
