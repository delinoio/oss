"use client";

import { ChangeEvent, useEffect, useMemo, useRef, useState } from "react";

import { deliverCompletionResult } from "@/apps/remote-file-picker/completion-bridge";
import {
  PickerSource,
  RemoteFilePickerCompletionResult,
  RemoteFilePickerCompletionStatus,
  RemoteFilePickerRequest,
  SignedUrlProvider,
} from "@/apps/remote-file-picker/contracts";
import { parseRemoteFilePickerRequestFromSearch } from "@/apps/remote-file-picker/request-parser";
import { getPhaseOneSourceAdapters } from "@/apps/remote-file-picker/source-adapters";
import { uploadFileToSignedUrl } from "@/apps/remote-file-picker/upload-orchestrator";
import { LogEvent, logError, logInfo } from "@/lib/logger";

function providerLabel(provider: SignedUrlProvider): string {
  switch (provider) {
    case SignedUrlProvider.AwsS3:
      return "AWS S3";
    case SignedUrlProvider.GcpCloudStorage:
      return "GCP Cloud Storage";
    default:
      return provider;
  }
}

function validateFileAgainstConstraints(
  file: File,
  request: RemoteFilePickerRequest,
): string | undefined {
  const constraints = request.fileConstraints;
  if (!constraints) {
    return undefined;
  }

  if (constraints.maxBytes && file.size > constraints.maxBytes) {
    return `The selected file exceeds max size (${constraints.maxBytes} bytes).`;
  }

  if (constraints.allowedMimeTypes && constraints.allowedMimeTypes.length > 0) {
    if (!constraints.allowedMimeTypes.includes(file.type)) {
      return `The selected file type (${file.type || "unknown"}) is not allowed.`;
    }
  }

  return undefined;
}

function buildSuccessfulCompletionResult(
  request: RemoteFilePickerRequest,
  file: File,
): RemoteFilePickerCompletionResult {
  return {
    requestId: request.requestId,
    provider: request.uploadTarget.provider,
    status: RemoteFilePickerCompletionStatus.Success,
    uploadedAt: new Date().toISOString(),
    file: {
      name: file.name,
      sizeBytes: file.size,
      mimeType: file.type,
    },
  };
}

export function RemoteFilePickerApp() {
  const localFileInputRef = useRef<HTMLInputElement>(null);
  const mobileCameraInputRef = useRef<HTMLInputElement>(null);

  const [request, setRequest] = useState<RemoteFilePickerRequest | null>(null);
  const [requestError, setRequestError] = useState<string>("");
  const [selectedSource, setSelectedSource] = useState<PickerSource | null>(null);
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [uploadProgressPercent, setUploadProgressPercent] = useState<number>(0);
  const [uploadMessage, setUploadMessage] = useState<string>("");
  const [errorMessage, setErrorMessage] = useState<string>("");
  const [completionMessage, setCompletionMessage] = useState<string>("");
  const [isUploading, setIsUploading] = useState<boolean>(false);

  useEffect(() => {
    const parseResult = parseRemoteFilePickerRequestFromSearch(
      window.location.search,
      new Date(),
    );

    if (!parseResult.ok) {
      setRequestError(parseResult.error.message);
      logError({
        event: LogEvent.RemoteFilePickerRequestValidation,
        route: "/apps/remote-file-picker",
        outcome: "failed",
        message: parseResult.error.message,
        context: {
          validationErrorCode: parseResult.error.code,
        },
      });
      return;
    }

    setRequest(parseResult.value);
    logInfo({
      event: LogEvent.RemoteFilePickerRequestValidation,
      route: "/apps/remote-file-picker",
      requestId: parseResult.value.requestId,
      provider: parseResult.value.uploadTarget.provider,
      outcome: "success",
      message: "Validated host upload request.",
    });
  }, []);

  const adapters = useMemo(() => {
    if (!request) {
      return [];
    }
    return getPhaseOneSourceAdapters(request.allowedSources);
  }, [request]);

  const acceptedMimeTypes = useMemo(() => {
    if (!request?.fileConstraints?.allowedMimeTypes) {
      return undefined;
    }
    return request.fileConstraints.allowedMimeTypes.join(",");
  }, [request]);

  const openFilePicker = (source: PickerSource) => {
    if (!request) {
      return;
    }

    setSelectedSource(source);
    setErrorMessage("");

    logInfo({
      event: LogEvent.RemoteFilePickerSourceSelection,
      route: "/apps/remote-file-picker",
      requestId: request.requestId,
      source,
      outcome: "selected",
      message: "User selected upload source.",
    });

    if (source === PickerSource.LocalFile) {
      localFileInputRef.current?.click();
      return;
    }

    if (source === PickerSource.MobileCamera) {
      mobileCameraInputRef.current?.click();
    }
  };

  const handleFileChosen = (
    source: PickerSource,
    event: ChangeEvent<HTMLInputElement>,
  ) => {
    if (!request) {
      return;
    }

    const chosenFile = event.target.files?.[0];
    event.target.value = "";

    if (!chosenFile) {
      return;
    }

    const validationError = validateFileAgainstConstraints(chosenFile, request);
    if (validationError) {
      setErrorMessage(validationError);
      setSelectedFile(null);
      logError({
        event: LogEvent.RemoteFilePickerSourceAdapterFailure,
        route: "/apps/remote-file-picker",
        requestId: request.requestId,
        source,
        outcome: "validation-failure",
        message: validationError,
      });
      return;
    }

    setErrorMessage("");
    setUploadMessage("");
    setCompletionMessage("");
    setSelectedSource(source);
    setSelectedFile(chosenFile);
  };

  const handleUpload = async () => {
    if (!request || !selectedFile) {
      setErrorMessage("Select a file before upload.");
      return;
    }

    setIsUploading(true);
    setErrorMessage("");
    setCompletionMessage("");
    setUploadMessage("Uploading file...");
    setUploadProgressPercent(0);

    logInfo({
      event: LogEvent.RemoteFilePickerPreprocessDecision,
      route: "/apps/remote-file-picker",
      requestId: request.requestId,
      outcome: "skipped",
      message: "Image transform preprocessing is skipped in Phase 1.",
    });

    logInfo({
      event: LogEvent.RemoteFilePickerUploadStart,
      route: "/apps/remote-file-picker",
      requestId: request.requestId,
      provider: request.uploadTarget.provider,
      source: selectedSource || undefined,
      message: "Starting signed URL upload request.",
    });

    const uploadResult = await uploadFileToSignedUrl({
      file: selectedFile,
      target: request.uploadTarget,
      onProgress: (progress) => {
        setUploadProgressPercent(progress.percent);
      },
    });

    if (uploadResult.ok) {
      setUploadMessage("Upload complete.");

      logInfo({
        event: LogEvent.RemoteFilePickerUploadResult,
        route: "/apps/remote-file-picker",
        requestId: request.requestId,
        provider: request.uploadTarget.provider,
        statusCode: uploadResult.statusCode,
        outcome: "success",
        message: "Signed URL upload completed.",
      });

      const completionResult = buildSuccessfulCompletionResult(request, selectedFile);
      const deliveryResult = deliverCompletionResult(completionResult, request.callback);

      if (deliveryResult.delivered) {
        setCompletionMessage(deliveryResult.message);
      } else {
        setErrorMessage(deliveryResult.message);
      }

      logInfo({
        event: LogEvent.RemoteFilePickerCallbackResult,
        route: "/apps/remote-file-picker",
        requestId: request.requestId,
        outcome: deliveryResult.delivered ? "success" : "failed",
        channel: deliveryResult.channel,
        message: deliveryResult.message,
      });
    } else {
      setUploadMessage("");
      setErrorMessage(uploadResult.message);

      logError({
        event: LogEvent.RemoteFilePickerUploadResult,
        route: "/apps/remote-file-picker",
        requestId: request.requestId,
        provider: request.uploadTarget.provider,
        statusCode: uploadResult.statusCode,
        outcome: "failed",
        message: uploadResult.message,
        context: {
          uploadFailureCode: uploadResult.code,
        },
      });

      const completionResult: RemoteFilePickerCompletionResult = {
        requestId: request.requestId,
        provider: request.uploadTarget.provider,
        status: RemoteFilePickerCompletionStatus.Failure,
        uploadedAt: new Date().toISOString(),
        error: {
          code: uploadResult.code,
          message: uploadResult.message,
          httpStatus: uploadResult.statusCode,
        },
      };

      const deliveryResult = deliverCompletionResult(completionResult, request.callback);

      if (deliveryResult.delivered) {
        setCompletionMessage(deliveryResult.message);
      } else {
        setErrorMessage(
          `${uploadResult.message} Completion callback also failed: ${deliveryResult.message}`,
        );
      }

      logError({
        event: LogEvent.RemoteFilePickerCallbackResult,
        route: "/apps/remote-file-picker",
        requestId: request.requestId,
        outcome: deliveryResult.delivered ? "failed-upload-callback-success" : "failed",
        channel: deliveryResult.channel,
        message: deliveryResult.message,
      });
    }

    setIsUploading(false);
  };

  if (!request && !requestError) {
    return (
      <section aria-label="remote file picker" className="dk-stack">
        <div className="dk-card">
          <p className="dk-eyebrow">Upload Session</p>
          <h2 className="dk-section-title">Remote File Picker Upload</h2>
          <p className="dk-paragraph">Loading host upload request...</p>
        </div>
      </section>
    );
  }

  if (requestError) {
    return (
      <section aria-label="remote file picker" className="dk-stack">
        <div className="dk-card">
          <p className="dk-eyebrow">Upload Session</p>
          <h2 className="dk-section-title">Remote File Picker Upload</h2>
          <p role="alert" className="dk-alert">
            {requestError}
          </p>
        </div>
      </section>
    );
  }

  if (!request) {
    return null;
  }

  return (
    <section aria-label="remote file picker" className="dk-stack">
      <section className="dk-card" aria-label="upload request details">
        <p className="dk-eyebrow">Upload Session</p>
        <h2 className="dk-section-title">Remote File Picker Upload</h2>
        <p className="dk-paragraph">
          This flow uploads a selected file to the host-provided signed URL target.
        </p>

        <div className="dk-meta-grid">
          <div className="dk-meta-item">
            <p className="dk-meta-key">Request ID</p>
            <p className="dk-meta-value">
              <code className="dk-mono">{request.requestId}</code>
            </p>
          </div>
          <div className="dk-meta-item">
            <p className="dk-meta-key">Signed URL Provider</p>
            <p className="dk-meta-value">{providerLabel(request.uploadTarget.provider)}</p>
          </div>
        </div>
      </section>

      <section aria-label="source picker" className="dk-card">
        <h3 className="dk-subsection-title">Choose Source</h3>
        <p className="dk-subtle">Pick a source and choose exactly one file.</p>

        <div className="dk-source-list">
          {adapters.map((adapter) => (
            <div key={adapter.source} className="dk-source-item">
              <div className="dk-button-group">
                <button
                  type="button"
                  className="dk-button dk-button-secondary"
                  onClick={() => openFilePicker(adapter.source)}
                  disabled={isUploading}
                >
                  {adapter.buttonLabel}
                </button>
              </div>
              <p className="dk-source-description">{adapter.description}</p>
            </div>
          ))}
        </div>
      </section>

      <input
        ref={localFileInputRef}
        type="file"
        hidden
        accept={acceptedMimeTypes}
        onChange={(event) => handleFileChosen(PickerSource.LocalFile, event)}
      />
      <input
        ref={mobileCameraInputRef}
        type="file"
        hidden
        accept={acceptedMimeTypes}
        capture="environment"
        onChange={(event) => handleFileChosen(PickerSource.MobileCamera, event)}
      />

      <section aria-label="upload summary" className="dk-card">
        <h3 className="dk-subsection-title">Upload Summary</h3>

        {selectedFile ? (
          <p className="dk-subtle">
            Selected file: <strong>{selectedFile.name}</strong> ({selectedFile.type || "unknown"},{" "}
            {selectedFile.size} bytes)
          </p>
        ) : (
          <p className="dk-empty">No file selected yet.</p>
        )}

        <div className="dk-button-group">
          <button
            type="button"
            className="dk-button"
            disabled={!selectedFile || isUploading}
            onClick={() => {
              void handleUpload();
            }}
          >
            {isUploading ? "Uploading..." : "Upload to signed URL"}
          </button>
        </div>

        {uploadProgressPercent > 0 ? (
          <div className="dk-progress-wrap">
            <progress className="dk-progress" max={100} value={uploadProgressPercent} />
            <p className="dk-progress-label">Upload progress: {uploadProgressPercent}%</p>
          </div>
        ) : null}

        {uploadMessage ? <p className="dk-status">{uploadMessage}</p> : null}
        {completionMessage ? (
          <p role="status" className="dk-success">
            {completionMessage}
          </p>
        ) : null}
        {errorMessage ? (
          <p role="alert" className="dk-alert">
            {errorMessage}
          </p>
        ) : null}
      </section>
    </section>
  );
}
