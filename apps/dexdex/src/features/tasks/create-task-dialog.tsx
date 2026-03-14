import { useState } from "react";
import { X } from "lucide-react";
import { cn } from "../../lib/cn";

interface CreateTaskDialogProps {
  onClose: () => void;
}

export function CreateTaskDialog({ onClose }: CreateTaskDialogProps) {
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();

    if (!title.trim()) {
      return;
    }

    setSubmitting(true);
    // TODO: Call CreateUnitTask RPC via connect-query mutation
    console.log("Create unit task:", { title, description });

    // Simulate API call
    await new Promise((resolve) => setTimeout(resolve, 500));
    setSubmitting(false);
    onClose();
  }

  function handleBackdropClick(e: React.MouseEvent) {
    if (e.target === e.currentTarget) {
      onClose();
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-[var(--color-bg-dialog-overlay)]"
      onClick={handleBackdropClick}
      role="dialog"
      aria-modal="true"
      aria-label="Create task"
    >
      <div className="w-full max-w-lg bg-[var(--color-bg-primary)] border border-[var(--color-border-default)] rounded-lg shadow-xl">
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-3 border-b border-[var(--color-border-default)]">
          <h2 className="text-[14px] font-semibold text-[var(--color-text-primary)]">
            New Task
          </h2>
          <button
            onClick={onClose}
            className="flex items-center justify-center w-7 h-7 rounded text-[var(--color-text-tertiary)] hover:text-[var(--color-text-primary)] hover:bg-[var(--color-bg-hover)] transition-colors"
            type="button"
          >
            <X size={16} />
          </button>
        </div>

        {/* Form */}
        <form onSubmit={handleSubmit} className="p-5 space-y-4">
          <div>
            <label
              htmlFor="task-title"
              className="block text-[12px] font-medium text-[var(--color-text-secondary)] mb-1.5"
            >
              Title
            </label>
            <input
              id="task-title"
              type="text"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="Task title..."
              className="w-full px-3 py-2 text-[13px] bg-[var(--color-bg-secondary)] border border-[var(--color-border-default)] rounded text-[var(--color-text-primary)] placeholder:text-[var(--color-text-tertiary)] focus:outline-none focus:border-[var(--color-border-accent)] transition-colors"
              autoFocus
            />
          </div>

          <div>
            <label
              htmlFor="task-description"
              className="block text-[12px] font-medium text-[var(--color-text-secondary)] mb-1.5"
            >
              Description (optional)
            </label>
            <textarea
              id="task-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Describe what needs to be done..."
              className="w-full px-3 py-2 text-[13px] bg-[var(--color-bg-secondary)] border border-[var(--color-border-default)] rounded text-[var(--color-text-primary)] placeholder:text-[var(--color-text-tertiary)] focus:outline-none focus:border-[var(--color-border-accent)] resize-none transition-colors"
              rows={4}
            />
          </div>

          <div className="flex justify-end gap-2 pt-2">
            <button
              onClick={onClose}
              className="px-4 py-2 text-[13px] font-medium text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] transition-colors"
              type="button"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={!title.trim() || submitting}
              className={cn(
                "px-4 py-2 text-[13px] font-medium bg-[var(--color-bg-accent)] text-[var(--color-text-on-accent)] rounded hover:bg-[var(--color-bg-accent-hover)] transition-colors",
                (!title.trim() || submitting) && "opacity-50 cursor-not-allowed",
              )}
            >
              {submitting ? "Creating..." : "Create Task"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
