"use client";

import { useCallback, useState } from "react";
import { useMutation } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";

import {
  createSignedUploadUrl,
  confirmUpload,
} from "@/gen/remotefilepicker/v1/remote_file_picker-UploadService_connectquery";
import { StorageProvider, UploadStatus } from "@/gen/remotefilepicker/v1/remote_file_picker_pb";
import { LogEvent, logInfo, logError } from "@/lib/logger";
import { DevkitRoute } from "@/lib/mini-app-registry";

export interface UploadState {
  status: "idle" | "creating-url" | "uploading" | "confirming" | "completed" | "failed";
  progress: number;
  uploadId?: string;
  publicUrl?: string;
  error?: string;
}

export function useUpload() {
  const [state, setState] = useState<UploadState>({ status: "idle", progress: 0 });
  const createUrlMutation = useMutation(createSignedUploadUrl);
  const confirmMutation = useMutation(confirmUpload);

  const upload = useCallback(
    async (file: File, provider: StorageProvider = StorageProvider.S3, bucket = "uploads") => {
      setState({ status: "creating-url", progress: 0 });

      logInfo({
        event: LogEvent.RemoteFilePickerUploadStart,
        route: DevkitRoute.RemoteFilePicker,
        message: `Starting upload: ${file.name}`,
        context: { fileName: file.name, size: file.size, type: file.type },
      });

      try {
        const urlResponse = await createUrlMutation.mutateAsync({
          fileName: file.name,
          contentType: file.type,
          contentLength: BigInt(file.size),
          provider,
          bucket,
          keyPrefix: "uploads",
          metadata: {},
        });

        const uploadId = urlResponse.uploadId;
        const signedUrl = urlResponse.signedUrl;

        setState({ status: "uploading", progress: 0, uploadId });

        await new Promise<void>((resolve, reject) => {
          const xhr = new XMLHttpRequest();
          xhr.open("PUT", signedUrl, true);
          xhr.setRequestHeader("Content-Type", file.type);

          xhr.upload.onprogress = (event) => {
            if (event.lengthComputable) {
              const pct = Math.round((event.loaded / event.total) * 100);
              setState((prev) => ({ ...prev, progress: pct }));
            }
          };

          xhr.onload = () => {
            if (xhr.status >= 200 && xhr.status < 300) {
              resolve();
            } else {
              reject(new Error(`Upload failed with status ${xhr.status}`));
            }
          };

          xhr.onerror = () => reject(new Error("Upload network error"));
          xhr.send(file);
        });

        setState({ status: "confirming", progress: 100, uploadId });

        const confirmResponse = await confirmMutation.mutateAsync({ uploadId });

        if (confirmResponse.status === UploadStatus.COMPLETED) {
          const finalState: UploadState = {
            status: "completed",
            progress: 100,
            uploadId,
            publicUrl: confirmResponse.publicUrl,
          };
          setState(finalState);

          logInfo({
            event: LogEvent.RemoteFilePickerUploadResult,
            route: DevkitRoute.RemoteFilePicker,
            outcome: "success",
            message: `Upload completed: ${file.name}`,
          });
        } else {
          throw new Error("Upload confirmation failed");
        }
      } catch (err) {
        const errorMsg = err instanceof Error ? err.message : "Unknown error";
        setState((prev) => ({ ...prev, status: "failed", error: errorMsg }));

        logError({
          event: LogEvent.RemoteFilePickerUploadResult,
          route: DevkitRoute.RemoteFilePicker,
          outcome: "failure",
          error: err,
          message: `Upload failed: ${file.name}`,
        });
      }
    },
    [createUrlMutation, confirmMutation],
  );

  const reset = useCallback(() => {
    setState({ status: "idle", progress: 0 });
  }, []);

  return { state, upload, reset };
}
