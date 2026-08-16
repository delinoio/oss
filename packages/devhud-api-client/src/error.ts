import { Code, ConnectError } from "@connectrpc/connect";
import type { DescMessage, MessageShape } from "@bufbuild/protobuf";

import {
  ErrorMetadataSchema,
  PaginationFailureSchema,
  PermissionFailureSchema,
  QuotaFailureSchema,
  type PaginationFailure,
  type PermissionFailure,
  type QuotaFailure,
} from "./gen/devhud/v1/common_pb.js";
import {
  AccountFailureSchema,
  type AccountFailure,
} from "./gen/devhud/v1/account_pb.js";
import {
  SettingsRevisionConflictSchema,
  type SettingsRevisionConflict,
} from "./gen/devhud/v1/settings_pb.js";
import {
  UploadFailureSchema,
  type UploadFailure,
} from "./gen/devhud/v1/upload_pb.js";

export type DevHudClientError =
  | ErrorVariant<"unauthenticated">
  | ErrorVariant<"permissionDenied", PermissionFailure>
  | ErrorVariant<"revisionConflict", SettingsRevisionConflict>
  | ErrorVariant<"quotaExceeded", QuotaFailure>
  | ErrorVariant<"uploadPrecondition", UploadFailure>
  | ErrorVariant<"accountPrecondition", AccountFailure>
  | ErrorVariant<"pagination", PaginationFailure>
  | ErrorVariant<"unknown">;

type ErrorVariant<Kind extends string, Detail = undefined> = {
  kind: Kind;
  code: Code;
  correlationId: string | undefined;
  message: string;
  detail: Detail;
  cause: ConnectError;
};

export function mapDevHudError(reason: unknown): DevHudClientError {
  const error = ConnectError.from(reason);
  const correlationId = getCorrelationId(error);
  const common = {
    code: error.code,
    correlationId,
    message: error.rawMessage,
    cause: error,
  };

  if (error.code === Code.Unauthenticated) {
    return { kind: "unauthenticated", detail: undefined, ...common };
  }

  const permission = firstDetail(error, PermissionFailureSchema);
  if (error.code === Code.PermissionDenied && permission !== undefined) {
    return { kind: "permissionDenied", detail: permission, ...common };
  }

  const conflict = firstDetail(error, SettingsRevisionConflictSchema);
  if (error.code === Code.Aborted && conflict !== undefined) {
    return { kind: "revisionConflict", detail: conflict, ...common };
  }

  const quota = firstDetail(error, QuotaFailureSchema);
  if (error.code === Code.ResourceExhausted && quota !== undefined) {
    return { kind: "quotaExceeded", detail: quota, ...common };
  }

  const upload = firstDetail(error, UploadFailureSchema);
  if (error.code === Code.FailedPrecondition && upload !== undefined) {
    return { kind: "uploadPrecondition", detail: upload, ...common };
  }

  const account = firstDetail(error, AccountFailureSchema);
  if (error.code === Code.FailedPrecondition && account !== undefined) {
    return { kind: "accountPrecondition", detail: account, ...common };
  }

  const pagination = firstDetail(error, PaginationFailureSchema);
  if (error.code === Code.InvalidArgument && pagination !== undefined) {
    return { kind: "pagination", detail: pagination, ...common };
  }

  return { kind: "unknown", detail: undefined, ...common };
}

function firstDetail<Desc extends DescMessage>(
  error: ConnectError,
  schema: Desc,
): MessageShape<Desc> | undefined {
  return error.findDetails(schema)[0];
}

function getCorrelationId(error: ConnectError): string | undefined {
  const detail = error.findDetails(ErrorMetadataSchema)[0];
  return detail?.correlationId?.value ?? error.metadata.get("x-devhud-correlation-id") ?? undefined;
}
