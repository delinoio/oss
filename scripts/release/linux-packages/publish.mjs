import { encode, sha256, requireValue, safeKey, log } from './model.mjs';
import { validateCandidate } from './package.mjs';
import { validateRotation } from './keyring.mjs';

async function readJSON(store, key) {
  const value = await store.get(key);
  return value ? { ...value, value: JSON.parse(value.body) } : null;
}
export async function saveCandidate(state, record, packages) {
  validateCandidate(record);
  const key = `candidates/${record.identity.project}/${record.identity.version}.json`;
  const existing = await readJSON(state, key);
  if (existing) {
    validateCandidate(existing.value);
    requireValue(existing.value.source === record.source && JSON.stringify(existing.value.identity) === JSON.stringify(record.identity), 'PACKAGE_IDENTITY_CONFLICT');
    return existing.value;
  }
  for (const file of record.files) {
    const bytes = packages.find((entry) => entry.name === file.name)?.bytes;
    requireValue(bytes && sha256(bytes) === file.sha256, 'PACKAGE_CHECKSUM_MISMATCH');
    await state.put(`objects/${file.sha256}`, bytes, { immutable: true });
  }
  await state.put(key, encode(record), { immutable: true });
  return record;
}
export async function loadCandidate(state, plan, source) {
  const result = await readJSON(state, `candidates/${plan.project}/${plan.version}.json`);
  if (!result) return null;
  validateCandidate(result.value);
  requireValue(result.value.source === source && JSON.stringify(result.value.identity) === JSON.stringify(plan), 'PACKAGE_IDENTITY_CONFLICT');
  return result.value;
}
export async function addToCatalog(state, record, keyring) {
  const current = await readJSON(state, 'catalog.json');
  const catalog = current?.value ?? { schema_version: 1, candidates: [] };
  requireValue(catalog.schema_version === 1 && Array.isArray(catalog.candidates), 'INVALID_CATALOG');
  if (keyring) await validateRotation(state, catalog.keyring, keyring);
  const keyringChanged = keyring && JSON.stringify(catalog.keyring) !== JSON.stringify(keyring);
  if (keyring) catalog.keyring = keyring;
  const key = `candidates/${record.identity.project}/${record.identity.version}.json`;
  const candidate = { key, sha256: sha256(encode(record)) };
  const existing = catalog.candidates.find((entry) => entry.key === key);
  requireValue(!existing || existing.sha256 === candidate.sha256, 'CATALOG_IDENTITY_CONFLICT');
  if (!existing || keyringChanged) {
    if (!existing) catalog.candidates.push(candidate);
    catalog.candidates.sort((left, right) => left.key.localeCompare(right.key));
    await state.put('catalog.json', encode(catalog), { expectedETag: current?.etag ?? null });
  }
  const records = [];
  for (const entry of catalog.candidates) {
    requireValue(/^candidates\/[a-z-]+\/[0-9.]+\.json$/u.test(entry.key), 'INVALID_CATALOG_ENTRY');
    const value = await state.get(entry.key);
    requireValue(value && sha256(value.body) === entry.sha256, 'CATALOG_STORAGE_CORRUPT');
    records.push(validateCandidate(JSON.parse(value.body)));
  }
  return { catalog, records, generation: sha256(encode(catalog)) };
}
export async function snapshotFor(state, catalog, build, sign = (value) => value) {
  const key = `snapshots/${catalog.generation}.json`;
  const existing = await readJSON(state, key);
  if (existing) return validateSnapshot(existing.value, catalog.generation);
  const files = await build(catalog.records, async (file) => {
    const stored = await state.get(`objects/${file.sha256}`);
    requireValue(stored && sha256(stored.body) === file.sha256, 'PACKAGE_STORAGE_CORRUPT');
    return stored.body;
  }, catalog.generation);
  for (const file of files) {
    requireValue(sha256(file.bytes) === file.sha256, 'SNAPSHOT_CHECKSUM_MISMATCH');
    await state.put(`objects/${file.sha256}`, file.bytes, { immutable: true });
  }
  // Commit the complete signed snapshot before any public object is changed. Recovery
  // reuses its exact signatures and timestamps, including after a partially promoted repo.
  const snapshot = sign({ schema_version: 1, generation: catalog.generation, files: files.map(({ bytes: _bytes, ...file }) => file) });
  validateSnapshot(snapshot, catalog.generation);
  await state.put(key, encode(snapshot), { immutable: true });
  return snapshot;
}
function validateSnapshot(snapshot, generation) {
  requireValue(snapshot?.schema_version === 1 && snapshot.generation === generation && Array.isArray(snapshot.files) && snapshot.files.length > 0, 'INVALID_SNAPSHOT');
  requireValue(new Set(snapshot.files.map((file) => file.key)).size === snapshot.files.length, 'DUPLICATE_SNAPSHOT_KEY');
  for (const file of snapshot.files) {
    safeKey(file.key);
    requireValue(/^(?:apt\/|rpm\/|setup\/|keys\/)/u.test(file.key) && /^[a-f0-9]{64}$/u.test(file.sha256) && typeof file.mutable === 'boolean', 'INVALID_SNAPSHOT_FILE');
  }
  return snapshot;
}
export async function promote(state, publicStore, snapshot, verifyPublic = async () => {}) {
  validateSnapshot(snapshot, snapshot.generation);
  const currentCatalog = await state.get('catalog.json');
  requireValue(currentCatalog && sha256(currentCatalog.body) === snapshot.generation, 'STALE_PUBLICATION');
  const pointers = (file) => file.key.endsWith('/InRelease') || file.key.endsWith('/mirrorlist');
  const ordered = [...snapshot.files.filter((file) => !file.mutable), ...snapshot.files.filter((file) => file.mutable && !pointers(file)), ...snapshot.files.filter(pointers)];
  for (const file of ordered) {
    const value = await state.get(`objects/${file.sha256}`);
    requireValue(value && sha256(value.body) === file.sha256, 'SNAPSHOT_STORAGE_CORRUPT');
    await publicStore.put(file.key, value.body, { immutable: !file.mutable, cache: file.cache });
    await verifyPublic(file.key, value.body);
  }
  await state.put(`published/${snapshot.generation}.json`, encode({ schema_version: 1, generation: snapshot.generation, objects: ordered.map(({ key, sha256: hash }) => ({ key, sha256: hash })) }), { immutable: true });
  const keyring = JSON.parse(currentCatalog.body).keyring;
  if (keyring) {
    const key = `keyring-staged/${keyring.certificate}.json`;
    // Preserve the first fully verified public timestamp across retries/releases.
    if (!await state.get(key)) await state.put(key, encode({ schema_version: 1, signer: keyring.signer, timestamp: Date.now(), generation: snapshot.generation }), { immutable: true });
  }
  log('published', { generation: snapshot.generation, objects: ordered.length });
}
