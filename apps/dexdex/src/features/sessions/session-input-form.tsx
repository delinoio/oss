/**
 * Session input form for submitting input to a waiting session.
 * Supports Cmd/Ctrl+Enter to submit and Enter for newline.
 */

import { type CSSProperties, useCallback, useState } from "react";
import { useSubmitSessionInputMutation } from "../../hooks/use-dexdex-queries";

interface SessionInputFormProps {
  workspaceId: string;
  sessionId: string;
  onSubmitted?: () => void;
}

export function SessionInputForm({ workspaceId, sessionId, onSubmitted }: SessionInputFormProps) {
  const [inputText, setInputText] = useState("");
  const submitMutation = useSubmitSessionInputMutation();

  const handleSubmit = useCallback(() => {
    if (!inputText.trim()) return;
    submitMutation.mutate(
      {
        workspaceId,
        sessionId,
        inputText: inputText.trim(),
      },
      {
        onSuccess: () => {
          setInputText("");
          onSubmitted?.();
        },
      },
    );
  }, [workspaceId, sessionId, inputText, submitMutation, onSubmitted]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        handleSubmit();
      }
    },
    [handleSubmit],
  );

  const containerStyle: CSSProperties = {
    padding: "var(--space-4)",
    borderTop: "1px solid var(--color-border)",
  };

  const labelStyle: CSSProperties = {
    display: "block",
    fontSize: "var(--font-size-sm)",
    fontWeight: 600,
    color: "var(--color-text-primary)",
    marginBottom: "var(--space-2)",
  };

  const textareaStyle: CSSProperties = {
    width: "100%",
    padding: "var(--space-2)",
    fontSize: "var(--font-size-sm)",
    border: "1px solid var(--color-border)",
    borderRadius: "var(--radius-sm)",
    backgroundColor: "var(--color-bg-primary)",
    color: "var(--color-text-primary)",
    boxSizing: "border-box",
    resize: "vertical",
    fontFamily: "inherit",
  };

  const footerStyle: CSSProperties = {
    display: "flex",
    alignItems: "center",
    justifyContent: "space-between",
    marginTop: "var(--space-2)",
  };

  const hintStyle: CSSProperties = {
    fontSize: "var(--font-size-xs)",
    color: "var(--color-text-tertiary)",
  };

  const buttonStyle: CSSProperties = {
    padding: "4px 12px",
    fontSize: "var(--font-size-xs)",
    fontWeight: 500,
    border: "1px solid var(--color-accent)",
    borderRadius: "var(--radius-sm)",
    backgroundColor: "var(--color-accent)",
    color: "var(--color-text-inverse)",
    cursor: "pointer",
  };

  return (
    <div style={containerStyle} data-testid="session-input-form">
      <label style={labelStyle}>Session Input</label>
      <textarea
        value={inputText}
        onChange={(e) => setInputText(e.target.value)}
        onKeyDown={handleKeyDown}
        placeholder="Type your response to the agent..."
        rows={3}
        style={textareaStyle}
        data-testid="session-input-textarea"
      />
      <div style={footerStyle}>
        <span style={hintStyle}>Cmd+Enter to submit</span>
        <button
          style={buttonStyle}
          onClick={handleSubmit}
          disabled={!inputText.trim() || submitMutation.isPending}
          data-testid="session-input-submit"
        >
          {submitMutation.isPending ? "Submitting..." : "Submit"}
        </button>
      </div>
    </div>
  );
}
