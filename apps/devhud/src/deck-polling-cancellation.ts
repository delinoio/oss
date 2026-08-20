let cancellationGeneration = 0;

/** Invalidates in-flight Deck work before account-local data is purged. */
export function invalidateDeckPolling(): void {
  cancellationGeneration += 1;
}

export function deckPollingCancellationGeneration(): number {
  return cancellationGeneration;
}
