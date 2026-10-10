// SPDX-License-Identifier: Apache-2.0
// @vitest-environment node
import { beforeEach, expect, it, vi } from "vitest";
import { webcrypto } from "node:crypto";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { create, toJsonString } from "@bufbuild/protobuf";
import {
  CreateSessionRequestSchema,
  EnqueueInputRequestSchema,
  SteerQueuedInputRequestSchema,
  RespondQuestionRequestSchema,
  EntityKind,
  ErrorDetailSchema, FailureCode, SessionService, InteractionService, InboxService, DeviceService,
  RespondApprovalRequestSchema, SetInboxReadStateRequestSchema, SetNotificationPreferencesRequestSchema, ControlSessionRequestSchema,
  ClaimNotificationRequestSchema, ReportNotificationRequestSchema, RevokeDeviceRequestSchema,
} from "@delinoio/delidev-api-client";
import {
  ProtectedState,
  PendingAttempt,
  Operation,
  uuid,
  httpsOrigin,
  documentBytes,
} from "./state";
let raw: string | null;
beforeEach(() => {
  raw = null;
  vi.stubGlobal("crypto", webcrypto);
});
const store = {
  read: async () => raw,
  write: async (value: string) => {
    raw = value;
  },
};
const profile = () => ({
  id: uuid(),
  serverId: uuid(),
  deviceId: uuid(),
  name: "Owned",
  origin: "https://example.test",
  token: "a".repeat(43),
});
it("requires explicit HTTPS and rejects origins with credentials or paths", () => {
  for (const origin of [
    "http://127.0.0.1:123",
    "https://token@example.test",
    "https://example.test/path",
    "https://example.test/?key=secret",
  ])
    expect(() => httpsOrigin(origin)).toThrow();
  expect(httpsOrigin("https://example.test")).toBe("https://example.test");
});
it("stores original pairing before network I/O and reload preserves credential and request identities", async () => {
  let calls = 0;
  const state = new ProtectedState(store, async () => {
    calls++;
    throw new Error("lost");
  });
  const server = uuid(),
    pairing = uuid();
  const id = await state.preparePair(
    "Owned",
    "https://example.test",
    JSON.stringify({
      version: 1,
      server_id: server,
      pairing_id: pairing,
      endpoint: "https://example.test",
      code: "x".repeat(43),
    }),
  );
  const original = state.profile(id);
  expect(calls).toBe(0);
  await expect(state.pair(id)).rejects.toThrow();
  const resumed = new ProtectedState(store, async () => {
    calls++;
    throw new Error("lost");
  });
  await resumed.load();
  expect(resumed.profile(id)).toEqual(original);
  await expect(resumed.pair(id)).rejects.toThrow();
  expect(resumed.profile(id).pairing).toEqual(original.pairing);
  expect(calls).toBe(2);
});
for (const operation of [
  Operation.Create,
  Operation.Send,
  Operation.Steer,
  Operation.Question,
])
  it(`durably retains exact ${operation} protobuf and blocks replacement after response loss`, async () => {
    let calls = 0;
    const state = new ProtectedState(store, async () => {
        calls++;
        throw new Error("lost");
      }),
      p = profile();
    await state.update((s) => {
      s.profiles.push(p);
      s.selectedProfile = p.id;
    });
    const requestId = uuid(),
      session = uuid();
    const request =
      operation === Operation.Create
        ? create(CreateSessionRequestSchema, {
            requestId,
            documentJson: documentBytes({ name: "Owned", prompt: "draft" }),
          })
        : operation === Operation.Send
          ? create(EnqueueInputRequestSchema, {
              requestId,
              sessionId: session,
              documentJson: documentBytes({ prompt: "draft", mode: "execute" }),
            })
          : operation === Operation.Steer
            ? create(SteerQueuedInputRequestSchema, {
                mutation: { id: uuid(), expectedRevision: 8n, requestId },
                sessionId: session,
                expectedExecutionId: uuid(),
                expectedTurnId: uuid(),
              })
            : create(RespondQuestionRequestSchema, {
                mutation: { id: uuid(), expectedRevision: 5n, requestId },
                responseJson: documentBytes({
                  answers: { original: ["answer"] },
                }),
              });
    await expect(
      state.perform(p.id, operation, request, session),
    ).rejects.toThrow();
    const first = state.profile(p.id).pending;
    expect(first?.request).toContain(requestId);
    const reloaded = new ProtectedState(store, async () => {
      calls++;
      throw new Error("lost");
    });
    await reloaded.load();
    expect(reloaded.profile(p.id).pending).toEqual(first);
    await expect(
      reloaded.perform(p.id, operation, request, session),
    ).rejects.toThrow("pending-operation");
    expect(calls).toBe(1);
    await expect(reloaded.retry(p.id)).rejects.toThrow();
    expect(calls).toBe(2);
    expect(reloaded.profile(p.id).pending).toEqual(first);
  });
it("durable write failure prevents sending and changing selected profile", async () => {
  let calls = 0;
  const state = new ProtectedState(
    {
      read: async () => null,
      write: async () => {
        throw new Error("vault");
      },
    },
    async () => {
      calls++;
      throw new Error("network");
    },
  );
  const p = profile();
  state.state.profiles = [p];
  await expect(state.select(p.id)).rejects.toThrow();
  expect(state.state.selectedProfile).toBe("");
  await expect(
    state.perform(
      p.id,
      Operation.Send,
      create(EnqueueInputRequestSchema, {
        requestId: uuid(),
        sessionId: uuid(),
        documentJson: documentBytes({ prompt: "draft" }),
      }),
      uuid(),
    ),
  ).rejects.toThrow();
  expect(calls).toBe(0);
});
it("forgets only the explicit profile; does not revoke or stop remote execution", async () => {
  let calls = 0;
  const state = new ProtectedState(store, async () => {
      calls++;
      throw new Error();
    }),
    a = profile(),
    b = profile();
  await state.update((s) => {
    s.profiles = [a, b];
    s.selectedProfile = a.id;
  });
  await state.forget(a.id);
  expect(state.state.profiles).toEqual([b]);
  expect(state.state.selectedProfile).toBe("");
  expect(calls).toBe(0);
});

const rejectedOperations = [
  [Operation.Create,CreateSessionRequestSchema], [Operation.Send,EnqueueInputRequestSchema],
  [Operation.Steer,SteerQueuedInputRequestSchema], [Operation.Question,RespondQuestionRequestSchema],
  [Operation.Approval,RespondApprovalRequestSchema], [Operation.Read,SetInboxReadStateRequestSchema],
  [Operation.Preferences,SetNotificationPreferencesRequestSchema],
] as const;
function validationRequest(operation: Operation, requestId: string) {
 const mutation = {requestId, id: uuid(), expectedRevision: 8n};
 switch (operation) {
  case Operation.Create: return create(CreateSessionRequestSchema, {requestId, documentJson: documentBytes({name: "Owned", prompt: "corrected draft"})});
  case Operation.Send: return create(EnqueueInputRequestSchema, {requestId, sessionId: uuid(), documentJson: documentBytes({prompt: "corrected draft", mode: "execute"})});
  case Operation.Steer: return create(SteerQueuedInputRequestSchema, {mutation, sessionId: uuid(), expectedExecutionId: uuid(), expectedTurnId: uuid()});
  case Operation.Question: return create(RespondQuestionRequestSchema, {mutation, responseJson: documentBytes({answers: {original: ["answer"]}})});
  case Operation.Approval: return create(RespondApprovalRequestSchema, {mutation, responseJson: documentBytes({decision: "deny"})});
  case Operation.Read: return create(SetInboxReadStateRequestSchema, {mutation});
  case Operation.Preferences: return create(SetNotificationPreferencesRequestSchema, {requestId, expectedRevision: 8n, changes: {questions: true}});
  default: throw new Error("not-validation-operation");
 }
}
function versionedRejection(code = Code.InvalidArgument, detail: FailureCode = FailureCode.InvalidArgument) {
 return new ConnectError("Synthetic rejection",code,undefined,[{desc:ErrorDetailSchema,value:create(ErrorDetailSchema,{code:detail,guidance:"Correct the draft."})}]);
}
async function mutationFixture() {
 const state=new ProtectedState(store),p=profile();
 await state.update(s=>{s.profiles=[p];s.selectedProfile=p.id;});
 let rejection: unknown=versionedRejection();
 const call=vi.fn(async(_request: unknown)=>{if(rejection) throw rejection;return {};});
 const transport=createRouterTransport(router=>{
  router.service(SessionService,{createSession:call,enqueueInput:call,steerQueuedInput:call,controlSession:call});
  router.service(InteractionService,{respondQuestion:call,respondApproval:call});
  router.service(InboxService,{setInboxReadState:call,setNotificationPreferences:call,claimNotification:call,reportNotification:call});
  router.service(DeviceService,{revokeDevice:call});
 });
 vi.spyOn(state,"transport").mockReturnValue(transport);
 return {state,p,call,reject:(value:unknown)=>{rejection=value;}};
}
for(const [operation] of rejectedOperations) {
 it(`clears only a fresh versioned ${operation} validation rejection and permits a corrected draft`,async()=>{
  const f=await mutationFixture(),originalId=uuid(),request=validationRequest(operation,originalId),target=uuid();
  await expect(f.state.perform(f.p.id,operation,request,target)).rejects.toThrow();
  expect(f.state.profile(f.p.id).pending).toBeUndefined();
  expect(JSON.parse(raw!).profiles[0].pending).toBeUndefined();
  f.reject(undefined);
  const correctedId=uuid();
  await expect(f.state.perform(f.p.id,operation,validationRequest(operation,correctedId),target)).resolves.toBeDefined();
  expect(f.call.mock.calls[0]?.[0]).toMatchObject(operation===Operation.Preferences||operation===Operation.Create||operation===Operation.Send?{requestId:originalId}:{mutation:{requestId:originalId,expectedRevision:8n}});
  expect(f.call.mock.calls[1]?.[0]).toMatchObject(operation===Operation.Preferences||operation===Operation.Create||operation===Operation.Send?{requestId:correctedId}:{mutation:{requestId:correctedId,expectedRevision:8n}});
  expect(f.call).toHaveBeenCalledTimes(2);
 });
}
for(const provenance of [undefined,PendingAttempt.Sending,PendingAttempt.Uncertain]) {
 it(`retains ${provenance??"legacy"} original intent after apparent replay rejection`,async()=>{
  const f=await mutationFixture();
  await f.state.prepare(f.p.id,Operation.Send,create(EnqueueInputRequestSchema,{requestId:uuid(),sessionId:uuid(),documentJson:documentBytes({prompt:"draft"})}),uuid());
  await f.state.update(s=>{s.profiles[0]!.pending!.attempt=provenance;});
  const original=f.state.profile(f.p.id).pending!;
  await f.state.load();
  await expect(f.state.retry(f.p.id)).rejects.toThrow();
  expect(f.state.profile(f.p.id).pending).toEqual({...original,attempt:PendingAttempt.Uncertain});
 });
}
it("persists Sending before dispatch and retains lost-response provenance across rejection retries",async()=>{
 const f=await mutationFixture();
 f.reject(new ConnectError("Lost result",Code.Unavailable));
 f.call.mockImplementationOnce(async()=>{
  expect(JSON.parse(raw!).profiles[0].pending.attempt).toBe(PendingAttempt.Sending);
  throw new ConnectError("Lost result",Code.Unavailable);
 });
 await expect(f.state.perform(f.p.id,Operation.Send,create(EnqueueInputRequestSchema,{requestId:uuid(),sessionId:uuid()}),uuid())).rejects.toThrow();
 const original=f.state.profile(f.p.id).pending!;
 expect(original.attempt).toBe(PendingAttempt.Uncertain);
 for(const reason of [versionedRejection(),versionedRejection(Code.NotFound,FailureCode.NotFound),versionedRejection(Code.PermissionDenied,FailureCode.PermissionDenied),versionedRejection(Code.Aborted,FailureCode.Conflict)]) {
  f.reject(reason);await expect(f.state.retry(f.p.id)).rejects.toThrow();
  expect(f.state.profile(f.p.id).pending).toEqual(original);
 }
});
for(const reason of [new ConnectError("unversioned",Code.InvalidArgument),versionedRejection(Code.Internal,FailureCode.Internal),versionedRejection(Code.Canceled,FailureCode.Canceled),versionedRejection(Code.FailedPrecondition,FailureCode.RecoveryRequired),versionedRejection(Code.PermissionDenied,FailureCode.PermissionDenied),versionedRejection(Code.NotFound,FailureCode.NotFound),versionedRejection(Code.Aborted,FailureCode.Conflict)]) {
 it(`retains fresh intent for non-authoritative rejection ${reason.code}`,async()=>{
  const f=await mutationFixture();f.reject(reason);
  await expect(f.state.perform(f.p.id,Operation.Send,create(EnqueueInputRequestSchema,{requestId:uuid(),sessionId:uuid()}),uuid())).rejects.toThrow();
  expect(f.state.profile(f.p.id).pending?.attempt).toBe(PendingAttempt.Uncertain);
 });
}
for(const [operation,schema] of [[Operation.Control,ControlSessionRequestSchema],[Operation.Claim,ClaimNotificationRequestSchema],[Operation.Report,ReportNotificationRequestSchema],[Operation.Revoke,RevokeDeviceRequestSchema]] as const) {
 it(`does not clear non-allowlisted ${operation} on versioned InvalidArgument`,async()=>{
  const f=await mutationFixture();
  await expect(f.state.perform(f.p.id,operation,create(schema),uuid())).rejects.toThrow();
  expect(f.state.profile(f.p.id).pending?.attempt).toBe(PendingAttempt.Uncertain);
 });
}
it("retains a Sending request when durable rejection settlement fails",async()=>{
 const f=await mutationFixture();
 const write=vi.spyOn(store,"write");
 f.call.mockImplementationOnce(async()=>{write.mockRejectedValueOnce(new Error("protected write failed"));throw versionedRejection();});
 await expect(f.state.perform(f.p.id,Operation.Send,create(EnqueueInputRequestSchema,{requestId:uuid(),sessionId:uuid()}),uuid())).rejects.toThrow("mutation-recovery-required");
 expect(f.state.profile(f.p.id).pending?.attempt).toBe(PendingAttempt.Sending);
 expect(JSON.parse(raw!).profiles[0].pending.attempt).toBe(PendingAttempt.Sending);
 write.mockRestore();
});
for (const replacement of ["profile", "request"] as const) {
 it(`does not clear a replacement ${replacement} after an old rejection`, async () => {
  const f = await mutationFixture();
  let replaced: unknown;
  f.call.mockImplementationOnce(async () => {
   await f.state.update(s => {
    const p = s.profiles[0]!;
    if (replacement === "profile") p.serverId = uuid();
    else p.pending = {
     operation: Operation.Send,
     request: toJsonString(EnqueueInputRequestSchema, create(EnqueueInputRequestSchema, {requestId: uuid(), sessionId: uuid()})),
     target: uuid(), attempt: PendingAttempt.Prepared,
    };
   });
   replaced = f.state.profile(f.p.id);
   throw versionedRejection();
  });
  await expect(f.state.perform(f.p.id, Operation.Send, validationRequest(Operation.Send, uuid()), uuid())).rejects.toThrow("mutation-recovery-required");
  expect(f.state.profile(f.p.id)).toEqual(replaced);
 });
}

it("does not dispatch when the durable Sending transition fails", async () => {
 const f = await mutationFixture();
 await f.state.prepare(f.p.id, Operation.Send, validationRequest(Operation.Send, uuid()), uuid());
 const original = f.state.profile(f.p.id).pending;
 const write = vi.spyOn(store, "write").mockRejectedValueOnce(new Error("protected write failed"));
 await expect(f.state.retry(f.p.id)).rejects.toThrow("mutation-recovery-required");
 expect(f.call).not.toHaveBeenCalled();
 expect(f.state.profile(f.p.id).pending).toEqual(original);
 write.mockRestore();
});
it("retains a committed request after result observation or receipt settlement fails", async () => {
 const f = await mutationFixture();
 let committed = false;
 f.call.mockImplementationOnce(async () => {
  committed = true;
  throw versionedRejection(Code.PermissionDenied, FailureCode.PermissionDenied);
 });
 await expect(f.state.perform(f.p.id, Operation.Question, validationRequest(Operation.Question, uuid()), uuid())).rejects.toThrow();
 expect(committed).toBe(true);
 const original = f.state.profile(f.p.id).pending!;
 expect(original.attempt).toBe(PendingAttempt.Uncertain);
 const write = vi.spyOn(store, "write");
 f.call.mockImplementationOnce(async () => {
  write.mockRejectedValueOnce(new Error("protected write failed"));
  return {};
 });
 await expect(f.state.retry(f.p.id)).rejects.toThrow("mutation-recovery-required");
 expect(f.state.profile(f.p.id).pending).toEqual(original);
 write.mockRestore();
});
