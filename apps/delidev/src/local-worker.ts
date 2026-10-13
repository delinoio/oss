import { ownedMessage, useProductMessage, copy } from "./localization";
import { useEffect, useRef, useState } from "react";

export interface LocalWorkerProof {
  machineId: string;
  token: string;
}
export type ReadLocalWorkerProof = () => Promise<LocalWorkerProof>;
const id =
  /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
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
      if (
        !id.test(proof.machineId) ||
        !/^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/.test(proof.token) ||
        (machine && proof.machineId !== machine)
      )
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
