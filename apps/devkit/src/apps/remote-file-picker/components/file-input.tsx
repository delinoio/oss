"use client";

import { useCallback, useRef, useState } from "react";

export interface FileInputProps {
  onFileSelected: (file: File) => void;
  disabled?: boolean;
}

export function FileInput({ onFileSelected, disabled }: FileInputProps) {
  const [isDragOver, setIsDragOver] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const handleFile = useCallback(
    (file: File) => {
      onFileSelected(file);
    },
    [onFileSelected],
  );

  const handleDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      setIsDragOver(false);
      if (disabled) return;
      const file = e.dataTransfer.files[0];
      if (file) handleFile(file);
    },
    [disabled, handleFile],
  );

  const handleInputChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const file = e.target.files?.[0];
      if (file) handleFile(file);
    },
    [handleFile],
  );

  return (
    <div
      onDragOver={(e) => { e.preventDefault(); if (!disabled) setIsDragOver(true); }}
      onDragLeave={() => setIsDragOver(false)}
      onDrop={handleDrop}
      style={{
        border: `2px dashed ${isDragOver ? "#0c5fca" : "#d7e2ea"}`,
        borderRadius: "12px",
        padding: "2rem",
        textAlign: "center",
        backgroundColor: isDragOver ? "#eff6ff" : "#fafafa",
        cursor: disabled ? "default" : "pointer",
        opacity: disabled ? 0.5 : 1,
        transition: "all 0.2s",
      }}
      onClick={() => !disabled && fileInputRef.current?.click()}
      role="button"
      tabIndex={0}
      aria-label="Drop file or click to browse"
    >
      <input
        ref={fileInputRef}
        type="file"
        onChange={handleInputChange}
        style={{ display: "none" }}
        disabled={disabled}
      />
      <div style={{ fontSize: "2rem", marginBottom: "0.5rem" }}>
        {isDragOver ? "Drop here" : "Drop file here"}
      </div>
      <p style={{ margin: "0.5rem 0", color: "#64748b", fontSize: "0.875rem" }}>
        or click to browse files
      </p>
      <div style={{ display: "flex", gap: "0.5rem", justifyContent: "center", marginTop: "1rem" }}>
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            fileInputRef.current?.click();
          }}
          disabled={disabled}
          style={{
            padding: "0.4rem 1rem",
            backgroundColor: "#0c5fca",
            color: "#fff",
            border: "none",
            borderRadius: "6px",
            cursor: disabled ? "default" : "pointer",
            fontSize: "0.8rem",
          }}
        >
          Browse Files
        </button>
        <label
          style={{
            padding: "0.4rem 1rem",
            backgroundColor: "#f1f5f9",
            color: "#475569",
            border: "1px solid #d7e2ea",
            borderRadius: "6px",
            cursor: disabled ? "default" : "pointer",
            fontSize: "0.8rem",
          }}
        >
          Camera
          <input
            type="file"
            accept="image/*"
            capture="environment"
            onChange={handleInputChange}
            style={{ display: "none" }}
            disabled={disabled}
          />
        </label>
      </div>
    </div>
  );
}
