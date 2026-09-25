import { useEffect, useRef, useState } from "react";

export interface LocalWorkerProof { machineId: string; token: string }
export type ReadLocalWorkerProof = () => Promise<LocalWorkerProof>;
const id = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
// Proof is read afresh for each Local mutation and never placed in component
// state or a query cache. Only the exact pending RPC can retain it on uncertainty.
export function useLocalWorkerProof(read?: ReadLocalWorkerProof) {
  const alive = useRef(false), pending = useRef(false);
  const [busy, setBusy] = useState(false), [problem, setProblem] = useState("");
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const load = async (machine?: string): Promise<LocalWorkerProof | undefined> => {
    if (!alive.current || pending.current) return;
    pending.current = true; setBusy(true); setProblem("");
    try {
      if (!read) throw new Error("Local proof unavailable");
      const proof = await read();
      if (!id.test(proof.machineId) || !/^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/.test(proof.token) || (machine && proof.machineId !== machine)) throw new Error("Local proof changed");
      if (alive.current) return proof;
    } catch {
      if (alive.current) setProblem("This computer's paired Worker could not be verified for the selected server and machine. Check its local Worker registration and retry; another Worker will not be selected.");
    } finally {
      pending.current = false;
      if (alive.current) setBusy(false);
    }
  };
  return { load, busy, problem, available: Boolean(read) };
}
