import {
  fromBinary,
  toBinary,
  type DescMessage,
  type MessageShape,
} from "@bufbuild/protobuf";
import { invoke } from "@tauri-apps/api/core";

export enum RealQaProcedure {
  ListPresets = "list-presets",
  GetPreset = "get-preset",
  CreatePreset = "create-preset",
  UpdatePreset = "update-preset",
  DeletePreset = "delete-preset",
  DeleteFeatureData = "delete-feature-data",
  GetGitHubConnection = "get-github-connection",
  StartGitHubConnection = "start-github-connection",
  ListGitHubInstallations = "list-github-installations",
  DisconnectGitHubConnection = "disconnect-github-connection",
  ListRepositories = "list-repositories",
  GetRepositoryIssueSchema = "get-repository-issue-schema",
  ListSubmissions = "list-submissions",
  CreateSubmission = "create-submission",
  CreateImageUpload = "create-image-upload",
  FinalizeImageUpload = "finalize-image-upload",
  SubmitIssue = "submit-issue",
  GetSubmission = "get-submission",
  RebindSubmissionStorageAuthorization =
    "rebind-submission-storage-authorization",
  DeleteImage = "delete-image",
  DeleteSubmissionAssets = "delete-submission-assets",
}

export type RealQaTransportFailure =
  | "authentication-required"
  | "reauthentication-required"
  | "permission-denied"
  | "owner-scope-not-found"
  | "invalid-request"
  | "request-too-large"
  | "response-too-large"
  | "rate-limited"
  | "conflict"
  | "billing-required"
  | "service-unavailable"
  | "r2-unavailable"
  | "github-unavailable"
  | "stale-revision"
  | "image-too-large"
  | "session-too-large"
  | "decoded-image-too-large"
  | "final-body-too-large"
  | "upload-concurrency-limited"
  | "storage-billing-grace"
  | "submission-ambiguous"
  | "public-image-confirmation-required"
  | "upload-rejected";

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

/**
 * Invokes one exact generated RealQA protobuf procedure through native code.
 * No URL, HTTP method, headers, bearer, or generic transport authority crosses
 * the frontend boundary.
 */
export async function invokeRealQaProcedure<
  Input extends DescMessage,
  Output extends DescMessage,
>(
  procedure: RealQaProcedure,
  inputSchema: Input,
  outputSchema: Output,
  input: MessageShape<Input>,
): Promise<MessageShape<Output>> {
  const response = await invoke<NativeConnectResponse>("realqa_connect", {
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

export interface RealQaSignedPut {
  readonly signedPutUrl: string;
  readonly contentType: "image/png" | "image/webp";
  readonly sha256: string;
  readonly body: Uint8Array;
}

export function putRealQaImage(request: RealQaSignedPut): Promise<void> {
  return invoke<void>("realqa_signed_put", {
    request: {
      signedPutUrl: request.signedPutUrl,
      contentType: request.contentType,
      sha256: request.sha256,
      bodyBase64: bytesToBase64(request.body),
    },
  });
}

export function openRealQaGitHubAuthorization(target: string): Promise<void> {
  return invoke<void>("realqa_open_github_authorization", { target });
}

export { base64ToBytes, bytesToBase64 };
