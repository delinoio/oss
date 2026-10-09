// SPDX-License-Identifier: Apache-2.0
import { ImageMediaType } from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";
import { RetainedImages } from "./image-attachments";
import { retainedImages } from "./image-input";
const record = (value: unknown): Record<string, unknown> | undefined => value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
export function generationObservation(value: unknown) {
 const v = record(value);
 if (!v || Object.keys(v).some(key => !["status", "revised_prompt", "transparent_background", "failure", "outputs"].includes(key)) || !["in_progress", "completed", "failed"].includes(String(v.status)) || v.revised_prompt !== undefined && typeof v.revised_prompt !== "string" || v.transparent_background !== undefined && typeof v.transparent_background !== "boolean") return undefined;
 const outputs = retainedImages(v.outputs);
 if (!outputs || v.status === "completed" && (outputs.length !== 1 || outputs[0]?.mediaType !== ImageMediaType.PNG) || v.status !== "completed" && outputs.length !== 0) return undefined;
 const failure = v.failure === undefined ? undefined : record(v.failure);
 if (v.failure !== undefined && (!failure || Object.keys(failure).some(key => !["type", "limit_id", "resets_at"].includes(key)) || failure.type !== "usageLimitExceeded" || typeof failure.limit_id !== "string" || !failure.limit_id || failure.resets_at !== null && (!Number.isSafeInteger(failure.resets_at) || Number(failure.resets_at) < 0))) return undefined;
 if (v.status === "in_progress" && (v.revised_prompt !== undefined || v.transparent_background !== undefined || failure) || v.status === "completed" && failure || v.status === "failed" && v.transparent_background !== undefined) return undefined;
 return { status: v.status as "in_progress" | "completed" | "failed", prompt: v.revised_prompt as string | undefined, transparent: v.transparent_background as boolean | undefined, failure, outputs: v.outputs };
}
export function NativeImageGeneration({ artifact, sessionId, active, state }: { artifact: Record<string, unknown>; sessionId: string; active: boolean; state?: string }) {
 useLocale();
 const started = record(artifact.started), completed = record(artifact.completed);
 const initial = generationObservation(started?.image_generation), settled = completed ? generationObservation(completed.image_generation) : undefined;
 if (started?.kind !== "image-generation" || initial?.status !== "in_progress" || completed && (completed.kind !== "image-generation" || !settled || settled.status === "in_progress")) return <p role="status">{copy("image-input.evidenceUnavailable")}</p>;
 const observation = settled ?? initial;
 return <section aria-label={copy("image-input.generatedHeading")}><h3>{copy("image-input.generatedHeading")}</h3><p role="status">{copy(observation.status === "completed" ? "image-input.generatedCompleted" : observation.status === "failed" ? "image-input.generatedFailed" : state && state !== "streaming" ? "image-input.generatedUnsettled" : "image-input.generatedRunning")}</p>{observation.failure ? <p>{copy("image-input.generatedLimit")}</p> : null}{observation.prompt !== undefined ? <pre>{observation.prompt}</pre> : null}<RetainedImages value={observation.outputs} sessionId={sessionId} active={active} exportable /></section>;
}
