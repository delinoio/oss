/**
 * Create task dialog component.
 */

import { type CSSProperties, type FormEvent, useState } from "react";

interface CreateDialogProps {
  isOpen: boolean;
  onClose: () => void;
  onCreate: (title: string, description: string) => void;
}

export function CreateDialog({ isOpen, onClose, onCreate }: CreateDialogProps) {
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");

  if (!isOpen) return null;

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    const trimmed = title.trim();
    if (!trimmed) return;
    onCreate(trimmed, description.trim());
    setTitle("");
    setDescription("");
    onClose();
  }

  const overlayStyle: CSSProperties = {
    position: "fixed",
    inset: 0,
    backgroundColor: "var(--color-bg-overlay)",
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    zIndex: 90,
  };

  const dialogStyle: CSSProperties = {
    width: "min(480px, 90vw)",
    backgroundColor: "var(--color-bg-primary)",
    borderRadius: "var(--radius-lg)",
    boxShadow: "var(--shadow-overlay)",
    border: "1px solid var(--color-border)",
    padding: "var(--space-6)",
  };

  const inputStyle: CSSProperties = {
    width: "100%",
    padding: "var(--space-2) var(--space-3)",
    borderRadius: "var(--radius-md)",
    border: "1px solid var(--color-border)",
    fontSize: "var(--font-size-md)",
    backgroundColor: "var(--color-bg-secondary)",
    color: "var(--color-text-primary)",
    outline: "none",
  };

  const textareaStyle: CSSProperties = {
    ...inputStyle,
    minHeight: "80px",
    resize: "vertical",
    fontSize: "var(--font-size-base)",
  };

  return (
    <div style={overlayStyle} onClick={onClose} data-testid="create-dialog">
      <div style={dialogStyle} onClick={(e) => e.stopPropagation()}>
        <h2
          style={{
            fontSize: "var(--font-size-lg)",
            fontWeight: 600,
            marginBottom: "var(--space-4)",
          }}
        >
          Create Task
        </h2>
        <form onSubmit={handleSubmit}>
          <div style={{ marginBottom: "var(--space-3)" }}>
            <label
              htmlFor="task-title"
              style={{
                display: "block",
                fontSize: "var(--font-size-sm)",
                fontWeight: 500,
                marginBottom: "var(--space-1)",
                color: "var(--color-text-secondary)",
              }}
            >
              Title
            </label>
            <input
              id="task-title"
              style={inputStyle}
              type="text"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="Task title..."
              autoFocus
              data-testid="task-title-input"
            />
          </div>
          <div style={{ marginBottom: "var(--space-4)" }}>
            <label
              htmlFor="task-description"
              style={{
                display: "block",
                fontSize: "var(--font-size-sm)",
                fontWeight: 500,
                marginBottom: "var(--space-1)",
                color: "var(--color-text-secondary)",
              }}
            >
              Description
            </label>
            <textarea
              id="task-description"
              style={textareaStyle}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Describe the task..."
              data-testid="task-description-input"
            />
          </div>
          <div style={{ display: "flex", justifyContent: "flex-end", gap: "var(--space-2)" }}>
            <button
              type="button"
              onClick={onClose}
              style={{
                padding: "var(--space-2) var(--space-4)",
                borderRadius: "var(--radius-md)",
                fontSize: "var(--font-size-sm)",
                color: "var(--color-text-secondary)",
                border: "1px solid var(--color-border)",
              }}
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={!title.trim()}
              style={{
                padding: "var(--space-2) var(--space-4)",
                borderRadius: "var(--radius-md)",
                fontSize: "var(--font-size-sm)",
                fontWeight: 500,
                backgroundColor: title.trim() ? "var(--color-accent)" : "var(--color-bg-tertiary)",
                color: title.trim() ? "var(--color-text-inverse)" : "var(--color-text-tertiary)",
                cursor: title.trim() ? "pointer" : "not-allowed",
              }}
              data-testid="submit-create-task"
            >
              Create Task
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
