import {
  fromBinary,
  toBinary,
  type DescMessage,
  type MessageShape,
} from "@bufbuild/protobuf";
import { invoke } from "@tauri-apps/api/core";

export enum DeckProcedure {
  ListViews = "list-views",
  GetView = "get-view",
  CreateView = "create-view",
  UpdateView = "update-view",
  DeleteView = "delete-view",
  ListPullRequests = "list-pull-requests",
  ListPullRequestMutationCandidates = "list-pull-request-mutation-candidates",
  GetRefreshPreflight = "get-refresh-preflight",
  RefreshView = "refresh-view",
  MutatePullRequest = "mutate-pull-request",
  GetDevice = "get-device",
  RegisterDevice = "register-device",
  UpdateDevice = "update-device",
  UnregisterDevice = "unregister-device",
  ResolveNotificationEvent = "resolve-notification-event",
}
interface NativeConnectResponse {
  readonly bodyBase64: string;
}

function bytesToBase64(bytes: Uint8Array): string {
  let binary = "";
  for (let offset = 0; offset < bytes.length; offset += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000));
  }
  return btoa(binary);
}

function base64ToBytes(value: string): Uint8Array {
  const binary = atob(value);
  return Uint8Array.from(binary, (character) => character.charCodeAt(0));
}

/** Exact protobuf procedure selection; no URL, method, headers, or bearer cross IPC. */
export async function invokeDeckProcedure<
  Input extends DescMessage,
  Output extends DescMessage,
>(
  procedure: DeckProcedure,
  inputSchema: Input,
  outputSchema: Output,
  input: MessageShape<Input>,
): Promise<MessageShape<Output>> {
  const response = await invoke<NativeConnectResponse>("deck_connect", {
    request: {
      procedure,
      bodyBase64: bytesToBase64(toBinary(inputSchema, input)),
    },
  });
  return fromBinary(outputSchema, base64ToBytes(response.bodyBase64), {
    readUnknownFields: false,
    recursionLimit: 64,
  });
}

export function openDeckPullRequest(
  owner: string,
  repository: string,
  number: number,
): Promise<void> {
  return invoke<void>("deck_open_pull_request", { owner, repository, number });
}
