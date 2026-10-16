import { ownedMessage, useProductMessage, copy } from "./localization";
import { useCallback, useEffect, useRef, useState } from "react";

export interface LocalWorkerProof {
  machineId: string;
  token: string;
}
export type ReadLocalWorkerProof = () => Promise<LocalWorkerProof>;
const id =
  /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const proofToken = /^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/;
function validProof(proof: LocalWorkerProof): boolean {
  return id.test(proof.machineId) && proofToken.test(proof.token);
}
/** Closed lifecycle of the trusted native local-Worker identity observation. */
export enum LocalWorkerIdentityStatus {
  Pending = "pending",
  Verified = "verified",
  Unavailable = "unavailable",
}
export interface LocalWorkerIdentity {
  status: LocalWorkerIdentityStatus;
  /** Set only when verified. The proof token is never retained. */
  machineId?: string;
}
/**
 * One fresh native observation of this app's current local Worker machine.
 * Only the verified machine ID leaves this function; the proof token is dropped.
 * Rejects when the native proof is unavailable or malformed.
 */
export async function observeLocalWorkerMachine(
  read: ReadLocalWorkerProof | undefined,
): Promise<string> {
  if (!read) throw new Error("Local proof unavailable");
  const proof = await read();
  if (!validProof(proof)) throw new Error("Local proof changed");
  return proof.machineId;
}
/**
 * Observes which machine is this app's current local Worker, by trusted native
 * identity rather than by display name or connectivity. Pending and failed
 * observations grant no authority. Observations are re-read on refresh only.
 */
export function useLocalWorkerIdentity(
  read: ReadLocalWorkerProof | undefined,
  enabled: boolean,
) {
  const [identity, setIdentity] = useState<LocalWorkerIdentity>({
    status: read
      ? LocalWorkerIdentityStatus.Pending
      : LocalWorkerIdentityStatus.Unavailable,
  });
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!enabled) return;
    if (!read) {
      setIdentity({ status: LocalWorkerIdentityStatus.Unavailable });
      return;
    }
    let current = true;
    setIdentity({ status: LocalWorkerIdentityStatus.Pending });
    observeLocalWorkerMachine(read).then(
      (machineId) => {
        if (current)
          setIdentity({
            status: LocalWorkerIdentityStatus.Verified,
            machineId,
          });
      },
      () => {
        if (current)
          setIdentity({ status: LocalWorkerIdentityStatus.Unavailable });
      },
    );
    return () => {
      current = false;
    };
  }, [read, enabled, attempt]);
  const refresh = useCallback(() => setAttempt((value) => value + 1), []);
  return { identity, refresh };
}
// Proof is read afresh for each Local mutation and never placed in component
// state or a query cache. Only the exact pending RPC can retain it on uncertainty.
export function useLocalWorkerProof(read?: ReadLocalWorkerProof) {
  const alive = useRef(false),
    pending = useRef(false),
    epoch = useRef(0);
  const [busy, setBusy] = useState(false),
    [problem, setProblem] = useProductMessage("");
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const load = async (
    machine?: string,
  ): Promise<LocalWorkerProof | undefined> => {
    if (!alive.current || pending.current) return;
    pending.current = true;
    const original = epoch.current;
    setBusy(true);
    setProblem("");
    try {
      if (!read) throw new Error("Local proof unavailable");
      const proof = await read();
      if (!validProof(proof) || (machine && proof.machineId !== machine))
        throw new Error("Local proof changed");
      if (alive.current) return proof;
    } catch {
      // A read that outlived its draft keeps the proof slot busy, but its
      // failure belongs to the discarded draft and must not reach the next one.
      if (alive.current && epoch.current === original)
        setProblem(ownedMessage("local-worker.extra.e16d869b57cc"));
    } finally {
      pending.current = false;
      if (alive.current) setBusy(false);
    }
  };
  // Detaches the read in flight from the current draft: it still holds the
  // proof slot until it settles, but its failure is no longer presented.
  const discard = () => {
    epoch.current += 1;
    setProblem("");
  };
  return { load, discard, busy, problem, available: Boolean(read) };
}
