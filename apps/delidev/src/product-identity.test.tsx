// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { ProductIdentity, ProductKind, PresentationNumbers, diagnosticText, presentationMetadata, presentationJSON, useProductLabels } from "./product-identity";
import { encode } from "./documents";

it("keeps presentation numbers stable through reorder and never recycles them",()=>{
 const labels=new PresentationNumbers(),first=newRequestId(),second=newRequestId();
 expect(labels.number(first)).toBe(1);expect(labels.number(second)).toBe(2);
 expect(labels.number(second)).toBe(2);expect(labels.number(first)).toBe(1);
 expect(labels.number(newRequestId())).toBe(3);
});
it("uses cached exact kind/name without new queries and preserves UUID-shaped user names",()=>{
 const client=new QueryClient(),id=newRequestId(),userName=newRequestId();
 client.setQueryData(["existing"],{resource:create(ResourceSchema,{id,kind:EntityKind.PROJECT,schemaVersion:1,revision:1n,documentJson:encode({name:userName})})});
 const mounted=render(<QueryClientProvider client={client}><ProductIdentity id={id} kind={ProductKind.Project}/></QueryClientProvider>);
 expect(screen.getByText(`${userName} · 1`)).toBeTruthy();expect(client.getQueryCache().getAll()).toHaveLength(1);
 mounted.rerender(<QueryClientProvider client={client}><ProductIdentity id={id} kind={ProductKind.Account}/></QueryClientProvider>);
 expect(screen.getByText("Account name unavailable · 1")).toBeTruthy();expect(client.getQueryCache().getAll()).toHaveLength(1);
});
it("rewrites only explicit application diagnostics and retains original records",()=>{
 const id=newRequestId(),record={message:`Target ${id} was unavailable. Inspect the original owner.`,correlationId:id};
 expect(diagnosticText(record.message)).toBe("Target [private reference] was unavailable. Inspect the original owner.");
 expect(record.correlationId).toBe(id);expect(record.message).toContain(id);
});
it("omits internal metadata UUIDs while preserving user/native artifact subtrees",()=>{
 const id=newRequestId(),record={session_id:id,execution_id:id,state:"failed",prompt:id,name:id,native:{thread_id:id,text:id},output:{text:id},metadata:{source:id}};
 expect(presentationMetadata(record)).toEqual({state:"failed",prompt:id,name:id,native:{thread_id:id,text:id},output:{text:id},metadata:{source:id}});
 expect(record.session_id).toBe(id);
});

it("retains observed names through payload eviction without sharing names across kinds", () => {
  const client = new QueryClient(), id = newRequestId();
  client.setQueryData(["current"], { resource: create(ResourceSchema, { id, kind: EntityKind.REPOSITORY, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Original repository" }) }) });
  const view = render(<QueryClientProvider client={client}><ProductIdentity id={id} kind={ProductKind.Repository} /></QueryClientProvider>);
  expect(screen.getByText("Original repository · 1")).toBeTruthy();
  client.removeQueries();
  view.rerender(<QueryClientProvider client={client}><ProductIdentity id={id} kind={ProductKind.Repository} /></QueryClientProvider>);
  expect(screen.getByText("Original repository · 1")).toBeTruthy();
  view.rerender(<QueryClientProvider client={client}><ProductIdentity id={id} kind={ProductKind.Account} /></QueryClientProvider>);
  expect(screen.getByText("Account name unavailable · 1")).toBeTruthy();
});

it("projects preview identity fields while preserving exact numbers and user strings", () => {
  const id = newRequestId();
  const original = `{"source_id":"${id}","expected_revision":9007199254740993,"after":{"name":"${id}","repositories":["${id}"],"contents":"${id}","native":{"id":"${id}"}}}`;
  const projected = presentationJSON(original);
  expect(projected).toContain('"expected_revision":9007199254740993');
  expect(projected).toContain('"source_id":"[private reference]"');
  expect(projected).toContain('"repositories":["[private reference]"]');
  expect(projected).toContain(`"name":"${id}"`);
  expect(projected).toContain(`"contents":"${id}"`);
  expect(projected).toContain(`"native":{"id":"${id}"}`);
  expect(original).toContain(`"source_id":"${id}"`);
});

it("keeps same-name actions bound to their original identities after reorder", () => {
  const client = new QueryClient(), first = newRequestId(), second = newRequestId();
  const selected: string[] = [];
  function Rows({ ids }: { ids: string[] }) {
    const label = useProductLabels();
    return <>{ids.map(id => <button key={id} onClick={() => selected.push(id)}>{label(id, ProductKind.Repository, "Duplicate")}</button>)}</>;
  }
  const view = render(<QueryClientProvider client={client}><Rows ids={[first, second]} /></QueryClientProvider>);
  view.rerender(<QueryClientProvider client={client}><Rows ids={[second, first]} /></QueryClientProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Duplicate · 1" }));
  fireEvent.click(screen.getByRole("button", { name: "Duplicate · 2" }));
  expect(selected).toEqual([first, second]);
  expect(view.container.textContent).not.toContain(first);
  expect(view.container.textContent).not.toContain(second);
  expect(client.getQueryCache().getAll()).toHaveLength(0);
});
