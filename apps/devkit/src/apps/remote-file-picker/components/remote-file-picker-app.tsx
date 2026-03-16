"use client";

import { useState } from "react";

import { RemoteFilePickerTransportProvider } from "@/apps/remote-file-picker/hooks/use-remote-file-picker-transport";
import { useUpload } from "@/apps/remote-file-picker/hooks/use-upload";
import { FileInput } from "./file-input";
import { UploadProgress } from "./upload-progress";
import { UploadResult } from "./upload-result";

function RemoteFilePickerContent() {
  const { state, upload, reset } = useUpload();
  const [selectedFile, setSelectedFile] = useState<File | undefined>(undefined);

  const handleFileSelected = (file: File) => {
    setSelectedFile(file);
    upload(file);
  };

  const handleReset = () => {
    setSelectedFile(undefined);
    reset();
  };

  const isUploading =
    state.status === "creating-url" ||
    state.status === "uploading" ||
    state.status === "confirming";

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1.5rem" }}>
      {state.status === "completed" ? (
        <UploadResult
          state={state}
          fileName={selectedFile?.name}
          onReset={handleReset}
        />
      ) : (
        <>
          <FileInput onFileSelected={handleFileSelected} disabled={isUploading} />
          <UploadProgress state={state} fileName={selectedFile?.name} />
          {state.status === "failed" && (
            <button
              onClick={handleReset}
              style={{
                padding: "0.4rem 1rem",
                backgroundColor: "#f1f5f9",
                color: "#475569",
                border: "1px solid #d7e2ea",
                borderRadius: "6px",
                cursor: "pointer",
                fontSize: "0.8rem",
                alignSelf: "flex-start",
              }}
            >
              Try Again
            </button>
          )}
        </>
      )}
    </div>
  );
}

export function RemoteFilePickerApp() {
  return (
    <RemoteFilePickerTransportProvider>
      <RemoteFilePickerContent />
    </RemoteFilePickerTransportProvider>
  );
}
