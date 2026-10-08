// SPDX-License-Identifier: Apache-2.0
import { useRef, useState } from "react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, newRequestId } from "@delinoio/delidev-api-client";
import { useProjectPromptHistory } from "./project-prompt-history";
import { MutationIntents } from "./mutation";
const project = newRequestId();
const entry = (prompt: string, acceptanceSequence = 1n, projectId = project) => ({ id: newRequestId(), projectId, prompt, acceptanceSequence, acceptedAt: "2026-10-08T01:00:00Z" });
function Composer({ id = project, locked = false, supported = true }: { id?: string; locked?: boolean; supported?: boolean }) {
 const [value, setValue] = useState(" current\n한글 "); const textarea = useRef<HTMLTextAreaElement>(null);
 const history = useProjectPromptHistory({ projectId: id, enabled: supported, active: true, blocked: locked, textarea, replace: text => setValue(text) });
 return <><textarea aria-label="First message" ref={textarea} disabled={locked} value={value} onChange={event => { history.onEdit(); setValue(event.target.value); }} onKeyDown={history.onKeyDown} onCompositionStart={history.onCompositionStart} onCompositionEnd={history.onCompositionEnd}/>{history.feedback}<button>Ordinary create</button></>;
}
function fixture(read = vi.fn(async () => ({ entries: [entry("new", 2n), entry("old", 1n)] }))) {
 const clear = vi.fn(async (request: { projectId: string; requestId: string }) => ({ ...request, removedCount: 2 }));
 const transport = createRouterTransport(router => router.service(ConfigurationService, { listProjectPromptHistory: read, clearProjectPromptHistory: clear }));
 const client = new QueryClient();
 const tree = (id = project, locked = false, supported = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Composer id={id} locked={locked} supported={supported}/></MutationIntents></QueryClientProvider></TransportProvider>;
 return { read, clear, tree };
}
it("recalls only at collapsed boundaries and preserves exact draft, editing, IME and locks", async () => {
 const f = fixture(); const view = render(f.tree()); await waitFor(() => expect(f.read).toHaveBeenCalled()); await waitFor(() => expect(screen.queryByText("Loading prompt history…")).toBeNull());
 const field = screen.getByRole("textbox") as HTMLTextAreaElement; const draft = field.value;
 field.setSelectionRange(2, 2); fireEvent.keyDown(field, { key: "ArrowUp" }); expect(field.value).toBe(draft);
 field.setSelectionRange(0, 2); fireEvent.keyDown(field, { key: "ArrowUp" }); expect(field.value).toBe(draft);
 field.setSelectionRange(0, 0); fireEvent.keyDown(field, { key: "ArrowUp", ctrlKey: true }); expect(field.value).toBe(draft);
 fireEvent.compositionStart(field); fireEvent.keyDown(field, { key: "ArrowUp" }); expect(field.value).toBe(draft); fireEvent.compositionEnd(field);
 fireEvent.keyDown(field, { key: "ArrowUp" }); expect(field.value).toBe("new"); await waitFor(() => expect(field.selectionStart).toBe(0));
 fireEvent.keyDown(field, { key: "ArrowUp" }); expect(field.value).toBe("old"); field.setSelectionRange(3, 3); fireEvent.keyDown(field, { key: "ArrowDown" }); expect(field.value).toBe("new"); await waitFor(() => expect(field.selectionStart).toBe(3)); fireEvent.keyDown(field, { key: "ArrowDown" }); expect(field.value).toBe(draft); await waitFor(() => expect(field.selectionStart).toBe(0));
 view.rerender(f.tree(project, true)); fireEvent.keyDown(field, { key: "ArrowUp" }); expect(field.value).toBe(draft);
});
it("never replaces text from late reads and keeps unsupported ordinary creation available", async () => {
 let complete!: (value: { entries: ReturnType<typeof entry>[] }) => void;
 const f = fixture(vi.fn(() => new Promise<{ entries: ReturnType<typeof entry>[] }>(resolve => { complete = resolve; })));
 const view = render(f.tree()); await waitFor(() => expect(f.read).toHaveBeenCalled()); const field = screen.getByRole("textbox") as HTMLTextAreaElement;
 fireEvent.change(field, { target: { value: "edited draft" } }); view.rerender(f.tree(newRequestId(), false, false)); complete({ entries: [entry("old scope")] });
 await waitFor(() => expect(field.value).toBe("edited draft")); field.setSelectionRange(0, 0); fireEvent.keyDown(field, { key: "ArrowUp" }); expect(field.value).toBe("edited draft"); expect(screen.getByRole("button", { name: "Ordinary create" })).toHaveProperty("disabled", false);
});
it("requires explicit clear confirmation and verifies its receipt", async () => {
 const f = fixture(); render(f.tree()); await waitFor(() => expect(f.read).toHaveBeenCalled()); fireEvent.click(screen.getByRole("button", { name: "Clear prompt history" })); expect(f.clear).not.toHaveBeenCalled();
 expect(screen.getByText(/Source sessions and older backups stay unchanged/)).toBeDefined(); const buttons = screen.getAllByRole("button", { name: "Clear prompt history" }); fireEvent.click(buttons.at(-1)!);
 await waitFor(() => expect(f.clear).toHaveBeenCalledOnce()); expect(f.clear.mock.calls[0]![0]).toMatchObject({ projectId: project, confirmed: true });
});
