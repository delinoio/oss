// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { ProductIdentity, ProductKind, PresentationNumbers, diagnosticText, presentationMetadata } from "./product-identity";
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
