// SPDX-License-Identifier: Apache-2.0
// Compatibility-only props for older embedders. Product mutations do not read
// secondary Worker ownership credentials.
export interface LocalWorkerProof { machineId: string; token: string }
export type ReadLocalWorkerProof = () => Promise<LocalWorkerProof>;
