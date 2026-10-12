// SPDX-License-Identifier: Apache-2.0
export interface SuggestionGuard {
  sessionId: string;
  transport: object;
  generation: number;
  revision: bigint;
  draftGeneration: bigint;
}
export interface SuggestionState extends SuggestionGuard {
  active: boolean;
  supported: boolean;
  draft: string;
  images: number;
  skills: number;
  blocked: boolean;
  composing: boolean;
}
/** Selection grants only an unsent empty draft. Current ownership is rechecked synchronously. */
export function placeAssistantSuggestion(
  original: SuggestionGuard,
  current: SuggestionState,
  option: string,
  change: (value: string) => boolean | void,
): boolean {
  if (
    !current.active ||
    !current.supported ||
    current.blocked ||
    current.composing ||
    current.draft !== "" ||
    current.images !== 0 ||
    current.skills !== 0 ||
    current.sessionId !== original.sessionId ||
    current.transport !== original.transport ||
    current.generation !== original.generation ||
    current.revision !== original.revision ||
    current.draftGeneration !== original.draftGeneration ||
    new TextEncoder().encode(option).length > 262144 ||
    option.includes("\0")
  )
    return false;
  return change(option) !== false;
}
