import { useMemo, useState } from "react";

import {
  DECK_QUERY_FIELDS,
  addDeckQueryClause,
  parseDeckQuery,
  removeDeckQueryClause,
  serializeDeckQuery,
  updateDeckQueryClause,
  type DeckQueryField,
} from "./query";

export function DeckQueryEditor({
  disabled = false,
  onChange,
  value,
}: {
  readonly disabled?: boolean;
  readonly onChange: (rawQuery: string) => void;
  readonly value: string;
}) {
  const [mode, setMode] = useState<"builder" | "raw">("builder");
  const parsed = useMemo(() => parseDeckQuery(value), [value]);
  return (
    <fieldset className="deck-query-editor" disabled={disabled}>
      <legend>GitHub pull request query</legend>
      <div aria-label="Query editing mode" className="segmented-control" role="group">
        <button
          aria-pressed={mode === "builder"}
          onClick={() => setMode("builder")}
          type="button"
        >
          Visual builder
        </button>
        <button
          aria-pressed={mode === "raw"}
          onClick={() => setMode("raw")}
          type="button"
        >
          Raw query
        </button>
      </div>
      {mode === "raw" ? (
        <label className="field" htmlFor="deck-raw-query">
          Raw GitHub query
          <textarea
            id="deck-raw-query"
            onChange={(event) => onChange(event.target.value)}
            rows={4}
            value={value}
          />
        </label>
      ) : (
        <div className="deck-builder">
          {parsed.clauses.map((clause) => (
            <div className="deck-builder-row" key={clause.id}>
              <label>
                <span className="visually-hidden">Clause type</span>
                <select
                  aria-label="Clause type"
                  onChange={(event) =>
                    onChange(serializeDeckQuery(updateDeckQueryClause(parsed, clause.id, {
                      ...clause,
                      field: event.target.value as DeckQueryField,
                    })))
                  }
                  value={clause.field}
                >
                  {DECK_QUERY_FIELDS.map((field) => (
                    <option key={field} value={field}>{field}</option>
                  ))}
                </select>
              </label>
              <label>
                <span className="visually-hidden">Clause value</span>
                <input
                  aria-label={`${clause.field} value`}
                  onChange={(event) =>
                    onChange(serializeDeckQuery(updateDeckQueryClause(parsed, clause.id, {
                      ...clause,
                      value: event.target.value,
                    })))
                  }
                  value={clause.value}
                />
              </label>
              <label className="deck-negated">
                <input
                  checked={clause.negated}
                  onChange={(event) =>
                    onChange(serializeDeckQuery(updateDeckQueryClause(parsed, clause.id, {
                      ...clause,
                      negated: event.target.checked,
                    })))
                  }
                  type="checkbox"
                />
                Exclude
              </label>
              <button
                aria-label={`Remove ${clause.field} clause`}
                className="text-button"
                onClick={() => onChange(serializeDeckQuery(removeDeckQueryClause(parsed, clause.id)))}
                type="button"
              >
                Remove
              </button>
            </div>
          ))}
          <button
            className="secondary-button"
            onClick={() => onChange(serializeDeckQuery(addDeckQueryClause(parsed)))}
            type="button"
          >
            Add filter
          </button>
          {parsed.unknownClauses.length > 0 ? (
            <div className="deck-unknown-clauses" role="note">
              <strong>Preserved raw clauses</strong>
              <p>{parsed.unknownClauses.join(" ")}</p>
              <p className="muted">Builder edits keep these clauses unchanged.</p>
            </div>
          ) : null}
        </div>
      )}
    </fieldset>
  );
}
