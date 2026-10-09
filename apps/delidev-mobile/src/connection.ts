// SPDX-License-Identifier: Apache-2.0
import { createClient, Code, ConnectError } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import {
  EntityKind,
  SystemService,
  ResourceService,
  synchronizeResources,
  SyncKind,
  ConnectionState,
  FailureCode,
} from "@delinoio/delidev-api-client";
import { tls } from "./platform";
import { ProtectedState } from "./state";
export enum Status {
  Connecting = "connecting",
  Live = "live",
  Suspended = "suspended",
  Unavailable = "unavailable",
  Revoked = "revoked",
  Version = "version",
  Certificate = "certificate",
}
export class Connection {
  readonly cache = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: 30_000, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  private generation = 0;
  private controller?: AbortController;
  active = false;
  status = Status.Connecting;
  constructor(
    readonly state: ProtectedState,
    readonly id: string,
    private readonly changed: (status: Status) => void,
    private readonly synchronized: () => void = () => {},
  ) {}
  private set(status: Status) {
    this.status = status;
    this.changed(status);
  }
  suspend(): void {
    this.active = false;
    this.generation++;
    this.controller?.abort();
    void this.cache.cancelQueries();
    this.cache.clear();
    this.set(Status.Suspended);
  }
  async resume(): Promise<void> {
    this.controller?.abort();
    const generation = ++this.generation;
    const controller = (this.controller = new AbortController());
    this.active = true;
    this.set(Status.Connecting);
    try {
      const transport = this.state.transport(this.id);
      const status = await createClient(SystemService, transport).getStatus(
        {},
        { signal: controller.signal },
      );
      if (generation !== this.generation || controller.signal.aborted) return;
      if (
        status.serverId !== this.state.profile(this.id).serverId ||
        status.protocolVersion !== 1
      ) {
        this.active = false;
        this.set(Status.Version);
        return;
      }
      this.set(Status.Live);
      await this.cache.invalidateQueries();
      for await (const update of synchronizeResources(
        createClient(ResourceService, transport),
        { kind: EntityKind.SETTINGS },
        {
          signal: controller.signal,
          watchKinds: [
            EntityKind.SESSION,
            EntityKind.MESSAGE,
            EntityKind.INTERACTION,
            EntityKind.INBOX,
            EntityKind.QUEUE,
          ],
          maxResources: 200,
          maxDocumentBytes: 2 << 20,
        },
      )) {
        if (generation !== this.generation || controller.signal.aborted) return;
        if (update.kind === SyncKind.Connection) {
          this.set(
            update.state === ConnectionState.Live
              ? Status.Live
              : update.failure?.code === FailureCode.Unauthenticated
                ? Status.Revoked
                : update.state === ConnectionState.Failed
                  ? Status.Unavailable
                  : Status.Connecting,
          );
          if (update.state === ConnectionState.Failed) {
            this.active = false;
            return;
          }
          continue;
        }
        // Indexed history remains bounded, paginated reads. Events invalidate only observations.
        await this.cache.invalidateQueries();
        this.synchronized();
      }
    } catch (error) {
      if (generation !== this.generation || controller.signal.aborted) return;
      this.active = false;
      let state =
        ConnectError.from(error).code === Code.Unauthenticated
          ? Status.Revoked
          : Status.Unavailable;
      if (state === Status.Unavailable)
        try {
          if ((await tls(this.state.profile(this.id).origin)) === "certificate")
            state = Status.Certificate;
        } catch {}
      if (generation === this.generation && !controller.signal.aborted)
        this.set(state);
    }
  }
  canMutate(): boolean {
    return (
      this.active &&
      this.status === Status.Live &&
      !this.state.profile(this.id).pending
    );
  }
}
