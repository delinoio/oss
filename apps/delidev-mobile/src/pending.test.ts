// SPDX-License-Identifier: Apache-2.0
// @vitest-environment node
import { beforeEach, expect, it, vi } from "vitest";
import { webcrypto } from "node:crypto";
import { create, fromJsonString, toJsonString, type DescMessage } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { FailureCode, ErrorDetailSchema, SessionService, InteractionService, InboxService, DeviceService, CreateSessionRequestSchema, EnqueueInputRequestSchema, SteerQueuedInputRequestSchema, RespondQuestionRequestSchema, RespondApprovalRequestSchema, SetInboxReadStateRequestSchema, SetNotificationPreferencesRequestSchema, ControlSessionRequestSchema, ClaimNotificationRequestSchema, ReportNotificationRequestSchema, RevokeDeviceRequestSchema } from "@delinoio/delidev-api-client";
import { ProtectedState, Operation, PendingPhase, uuid } from "./state";
beforeEach(() => { vi.stubGlobal("crypto", webcrypto); vi.spyOn(console, "warn").mockImplementation(() => { }); });
const schemas: Record<Operation, DescMessage> = { create: CreateSessionRequestSchema, send: EnqueueInputRequestSchema, steer: SteerQueuedInputRequestSchema,
  question: RespondQuestionRequestSchema, approval: RespondApprovalRequestSchema, read: SetInboxReadStateRequestSchema, preferences: SetNotificationPreferencesRequestSchema,
  control: ControlSessionRequestSchema, claim: ClaimNotificationRequestSchema, report: ReportNotificationRequestSchema, revoke: RevokeDeviceRequestSchema };
const allowed = [Operation.Create, Operation.Send, Operation.Steer, Operation.Question, Operation.Approval, Operation.Read, Operation.Preferences];
function request(operation: Operation) {
  return fromJsonString(schemas[operation], JSON.stringify([Operation.Steer, Operation.Question, Operation.Approval, Operation.Read].includes(operation)
    ? { mutation: { id: uuid(), requestId: uuid(), expectedRevision: "1" } } : { requestId: uuid() }));
}
function failure(code = FailureCode.InvalidArgument, transport = Code.InvalidArgument) {
  return new ConnectError("Original validation failure.", transport, undefined, [{ desc: ErrorDetailSchema, value: create(ErrorDetailSchema, { code, guidance: "Correct the original draft." }) }]);
}
async function harness() {
  let raw: string | null = null, failWrite = false;
  const storage = { read: async () => raw, write: async (value: string) => { if (failWrite)
      throw new Error("private storage diagnostic"); raw = value; } };
  const state = new ProtectedState(storage), profile = { id: uuid(), serverId: uuid(), deviceId: uuid(), name: "Owned", origin: "https://example.test", token: "a".repeat(43) };
  await state.update(s => { s.profiles.push(profile); s.selectedProfile = profile.id; });
  const dispatch = vi.fn(async () => { throw failure(); });
  const transport = createRouterTransport(router => {
    router.service(SessionService, { createSession: dispatch, enqueueInput: dispatch, steerQueuedInput: dispatch, controlSession: dispatch });
    router.service(InteractionService, { respondQuestion: dispatch, respondApproval: dispatch });
    router.service(InboxService, { setInboxReadState: dispatch, setNotificationPreferences: dispatch, claimNotification: dispatch, reportNotification: dispatch });
    router.service(DeviceService, { revokeDevice: dispatch });
  });
  vi.spyOn(state, "transport").mockReturnValue(transport);
  return { state, profile, dispatch, storage, failWrites: () => { failWrite = true; } };
}
for (const operation of allowed)
  it(`clears only fresh versioned ${operation} validation and keeps the draft`, async () => {
    const { state, profile, dispatch } = await harness(), draft = request(operation), original = toJsonString(schemas[operation], draft);
    dispatch.mockImplementationOnce(async () => { expect(state.profile(profile.id).pending?.phase).toBe(PendingPhase.Sending); throw failure(); });
    await expect(state.perform(profile.id, operation, draft, "original-target")).rejects.toThrow();
    expect(state.profile(profile.id).pending).toBeUndefined();
    expect(toJsonString(schemas[operation], draft)).toBe(original);
    const corrected = request(operation);
    expect(toJsonString(schemas[operation], corrected)).not.toBe(original);
    dispatch.mockImplementationOnce(async () => ({} as never));
    await state.perform(profile.id, operation, corrected, "original-target");
    expect(state.profile(profile.id).pending).toBeUndefined();
  });
for (const operation of [Operation.Control, Operation.Claim, Operation.Report, Operation.Revoke])
  it(`retains non-allowlisted ${operation}`, async () => {
    const { state, profile } = await harness();
    await expect(state.perform(profile.id, operation, create(schemas[operation]), "original")).rejects.toThrow();
    expect(state.profile(profile.id).pending?.phase).toBe(PendingPhase.Uncertain);
  });
for (const phase of [undefined, PendingPhase.Sending, PendingPhase.Uncertain])
  it(`retains restored ${phase ?? "legacy"} despite later validation`, async () => {
    const { state, profile, storage } = await harness();
    await state.prepare(profile.id, Operation.Create, request(Operation.Create), "original");
    await state.update(s => { s.profiles[0]!.pending!.phase = phase; });
    const resumed = new ProtectedState(storage);
    await resumed.load();
    vi.spyOn(resumed, "transport").mockImplementation(id => state.transport(id));
    const original = resumed.profile(profile.id).pending!;
    await expect(resumed.retry(profile.id)).rejects.toThrow();
    expect(resumed.profile(profile.id).pending).toEqual({ ...original, phase: PendingPhase.Uncertain });
  });
for (const [code, transport] of [[FailureCode.NotFound, Code.NotFound], [FailureCode.PermissionDenied, Code.PermissionDenied], [FailureCode.Conflict, Code.Aborted],
  [FailureCode.Internal, Code.Internal], [FailureCode.Unavailable, Code.Unavailable], [FailureCode.Canceled, Code.Canceled], [FailureCode.RecoveryRequired, Code.FailedPrecondition],
  [FailureCode.ServerUnavailable, Code.Unavailable]] as const)
  it(`retains uncertainty after ${code} then InvalidArgument`, async () => {
    const { state, profile, dispatch } = await harness();
    dispatch.mockRejectedValueOnce(failure(code, transport));
    await expect(state.perform(profile.id, Operation.Question, request(Operation.Question), "original")).rejects.toThrow();
    const original = state.profile(profile.id).pending;
    await expect(state.retry(profile.id)).rejects.toThrow();
    expect(state.profile(profile.id).pending).toEqual(original);
  });
it("retains unversioned rejection and inconsistent transport/detail codes", async () => {
  for (const reason of [new ConnectError("private text", Code.InvalidArgument), failure(FailureCode.InvalidArgument, Code.Unavailable)]) {
    const { state, profile, dispatch } = await harness();
    dispatch.mockRejectedValueOnce(reason);
    await expect(state.perform(profile.id, Operation.Create, request(Operation.Create), "original")).rejects.toThrow();
    expect(state.profile(profile.id).pending?.phase).toBe(PendingPhase.Uncertain);
  }
});
it("Sending write failure never dispatches", async () => {
  const { state, profile, dispatch, failWrites } = await harness();
  await state.prepare(profile.id, Operation.Create, request(Operation.Create), "original");
  const original = state.profile(profile.id).pending;
  failWrites();
  await expect(state.retry(profile.id)).rejects.toMatchObject({ code: Code.FailedPrecondition });
  expect(dispatch).not.toHaveBeenCalled();
  expect(state.profile(profile.id).pending).toEqual(original);
});
it("durable clear failure retains protected Sending and reports recovery", async () => {
  const { state, profile, dispatch, failWrites, storage } = await harness();
  dispatch.mockImplementationOnce(async () => { failWrites(); throw failure(); });
  await expect(state.perform(profile.id, Operation.Create, request(Operation.Create), "original")).rejects.toMatchObject({ code: Code.FailedPrecondition });
  const original = state.profile(profile.id).pending;
  expect(original?.phase).toBe(PendingPhase.Sending);
  const resumed = new ProtectedState(storage);
  await resumed.load();
  expect(resumed.profile(profile.id).pending).toEqual(original);
});
it("post-commit observation NotFound cannot clear an accepted original", async () => {
  const { state, profile, dispatch } = await harness();
  dispatch.mockRejectedValueOnce(failure(FailureCode.NotFound, Code.NotFound));
  await expect(state.perform(profile.id, Operation.Question, request(Operation.Question), "original")).rejects.toThrow();
  expect(state.profile(profile.id).pending?.phase).toBe(PendingPhase.Uncertain);
});
it("old rejection cannot clear replaced request or profile authority", async () => {
  for (const replace of ["request", "profile"] as const) {
    const { state, profile, dispatch } = await harness();
    dispatch.mockImplementationOnce(async () => {
      await state.update(s => { if (replace === "request")
        s.profiles[0]!.pending!.request = toJsonString(schemas[Operation.Create], request(Operation.Create));
      else
        s.profiles[0]!.token = "b".repeat(43); });
      throw failure();
    });
    await expect(state.perform(profile.id, Operation.Create, request(Operation.Create), "original")).rejects.toMatchObject({ code: Code.FailedPrecondition });
    expect(state.profile(profile.id).pending).toBeDefined();
    if (replace === "profile")
      expect(state.profile(profile.id).token).toBe("b".repeat(43));
  }
});
it("lost/unknown outcomes remain original uncertainty and logs contain only classifications", async () => {
  for (const reason of [new Error("private lost response"), failure("future_code" as FailureCode)]) {
    const { state, profile, dispatch } = await harness();
    dispatch.mockRejectedValueOnce(reason);
    await expect(state.perform(profile.id, Operation.Create, request(Operation.Create), "private-target")).rejects.toThrow();
    const original = state.profile(profile.id).pending;
    await expect(state.retry(profile.id)).rejects.toThrow();
    expect(state.profile(profile.id).pending).toEqual(original);
    expect(JSON.stringify(vi.mocked(console.warn).mock.calls)).not.toContain("private");
  }
});
it("uncertainty and successful receipt clear write failures retain Sending", async () => {
  for (const success of [false, true]) {
    const { state, profile, dispatch, failWrites } = await harness();
    dispatch.mockImplementationOnce(async () => { failWrites(); if (success)
      return {} as never; throw failure(FailureCode.Unavailable, Code.Unavailable); });
    await expect(state.perform(profile.id, Operation.Create, request(Operation.Create), "original")).rejects.toMatchObject({ code: Code.FailedPrecondition });
    expect(state.profile(profile.id).pending?.phase).toBe(PendingPhase.Sending);
  }
});
it("only the original profile is cleared and live dispatch cannot be duplicated", async () => {
  const { state, profile, dispatch } = await harness();
  let duplicate: Promise<unknown> | undefined;
  dispatch.mockImplementationOnce(async () => {
    duplicate = state.retry(profile.id);
    await expect(duplicate).rejects.toThrow("pending-operation");
    await state.update(s => { s.profiles.push({ ...profile, id: uuid(), deviceId: uuid(), pending: { operation: Operation.Create, request: "{}", target: "another", phase: PendingPhase.Prepared } }); s.selectedProfile = s.profiles[1]!.id; });
    throw failure();
  });
  await expect(state.perform(profile.id, Operation.Create, request(Operation.Create), "original")).rejects.toThrow();
  expect(dispatch).toHaveBeenCalledTimes(1);
  expect(state.profile(profile.id).pending).toBeUndefined();
  expect(state.state.profiles[1]!.pending?.target).toBe("another");
});
it("later apparent rejections never retire an original lost-response request", async () => {
  const { state, profile, dispatch } = await harness();
  dispatch.mockRejectedValueOnce(failure(FailureCode.Unavailable, Code.Unavailable));
  await expect(state.perform(profile.id, Operation.Create, request(Operation.Create), "original")).rejects.toThrow();
  const original = state.profile(profile.id).pending;
  for (const [code, transport] of [[FailureCode.InvalidArgument, Code.InvalidArgument], [FailureCode.NotFound, Code.NotFound], [FailureCode.PermissionDenied, Code.PermissionDenied], [FailureCode.Conflict, Code.Aborted]] as const) {
  dispatch.mockRejectedValueOnce(failure(code, transport));
  await expect(state.retry(profile.id)).rejects.toThrow();
  expect(state.profile(profile.id).pending).toEqual(original);
  }
});
