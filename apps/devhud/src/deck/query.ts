export const DECK_QUERY_FIELDS = [
  "owner",
  "repo",
  "author",
  "assignee",
  "review-requested",
  "team-review-requested",
  "label",
  "state",
  "draft",
  "base",
  "head",
  "review",
  "status",
  "updated",
] as const;

export type DeckQueryField = (typeof DECK_QUERY_FIELDS)[number];

export interface DeckQueryClause {
  readonly id: string;
  readonly field: DeckQueryField;
  readonly value: string;
  readonly negated: boolean;
}

export interface ParsedDeckQuery {
  readonly clauses: readonly DeckQueryClause[];
  readonly unknownClauses: readonly string[];
  readonly order: readonly DeckQueryTokenPosition[];
}

export type DeckQueryTokenPosition =
  | { readonly kind: "recognized"; readonly id: string }
  | { readonly kind: "unknown"; readonly index: number };

const recognizedFields = new Set<string>(DECK_QUERY_FIELDS);
const tokenPattern = /(?:[^\s"]+|"(?:\\.|[^"])*")+/gu;

function unquote(value: string): string {
  if (value.length >= 2 && value.startsWith('"') && value.endsWith('"')) {
    return value.slice(1, -1).replace(/\\([\\"])/gu, "$1");
  }
  return value;
}

function quote(value: string): string {
  return /\s|"/u.test(value)
    ? `"${value.replace(/([\\"])/gu, "\\$1")}"`
    : value;
}

export function parseDeckQuery(rawQuery: string): ParsedDeckQuery {
  const clauses: DeckQueryClause[] = [];
  const unknownClauses: string[] = [];
  const order: DeckQueryTokenPosition[] = [];
  const tokens = rawQuery.match(tokenPattern) ?? [];
  tokens.forEach((source, index) => {
    const negated = source.startsWith("-");
    const token = negated ? source.slice(1) : source;
    const separator = token.indexOf(":");
    if (separator <= 0) {
      order.push({ kind: "unknown", index: unknownClauses.length });
      unknownClauses.push(source);
      return;
    }
    const field = token.slice(0, separator);
    const value = unquote(token.slice(separator + 1));
    if (!recognizedFields.has(field) || value.length === 0) {
      order.push({ kind: "unknown", index: unknownClauses.length });
      unknownClauses.push(source);
      return;
    }
    const id = `clause-${index}`;
    clauses.push({
      id,
      field: field as DeckQueryField,
      value,
      negated,
    });
    order.push({ kind: "recognized", id });
  });
  return { clauses, unknownClauses, order };
}

/**
 * The original token order and every unknown token are retained. Builder edits
 * rewrite only their recognized token; newly added filters are appended.
 */
export function serializeDeckQuery(query: ParsedDeckQuery): string {
  const clausesById = new Map(query.clauses.map((clause) => [clause.id, clause]));
  const orderedIds = new Set<string>();
  const tokens = query.order.flatMap((position) => {
    if (position.kind === "unknown") {
      const source = query.unknownClauses[position.index];
      return source === undefined ? [] : [source];
    }
    orderedIds.add(position.id);
    const clause = clausesById.get(position.id);
    return clause === undefined
      ? []
      : [`${clause.negated ? "-" : ""}${clause.field}:${quote(clause.value)}`];
  });
  for (const clause of query.clauses) {
    if (!orderedIds.has(clause.id)) {
      tokens.push(`${clause.negated ? "-" : ""}${clause.field}:${quote(clause.value)}`);
    }
  }
  return tokens.join(" ");
}

export function updateDeckQueryClause(
  query: ParsedDeckQuery,
  id: string,
  update: Pick<DeckQueryClause, "field" | "value" | "negated">,
): ParsedDeckQuery {
  return {
    ...query,
    clauses: query.clauses.map((clause) =>
      clause.id === id ? { ...clause, ...update } : clause,
    ),
  };
}

export function addDeckQueryClause(
  query: ParsedDeckQuery,
  field: DeckQueryField = "repo",
): ParsedDeckQuery {
  let suffix = query.clauses.length;
  while (query.clauses.some((clause) => clause.id === `new-${suffix}`)) suffix += 1;
  return {
    ...query,
    clauses: [
      ...query.clauses,
      { id: `new-${suffix}`, field, value: "owner/repository", negated: false },
    ],
  };
}

export function removeDeckQueryClause(
  query: ParsedDeckQuery,
  id: string,
): ParsedDeckQuery {
  return { ...query, clauses: query.clauses.filter((clause) => clause.id !== id) };
}
