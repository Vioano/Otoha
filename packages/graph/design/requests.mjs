import fs from 'node:fs';
import path from 'node:path';
import { digest, loadModel, evaluateEvidence, problem } from './model.mjs';

const stages = ['submitted', 'accepted', 'implemented', 'tests_passed', 'integrated'];
function bounded(value, name, limit = 100) {
  if (typeof value !== 'string' || !value.trim() || value.length > limit) problem('request/input', `Invalid ${name}`);
  return value;
}
function identity(value, name) {
  bounded(value, name);
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_-]*$/.test(value)) problem('request/input', `Invalid ${name}`);
  return value;
}
export function journalPath(input, stateDir) {
  return path.join(stateDir || path.join(path.dirname(path.resolve(input)), '.archify-design', digest(path.resolve(input)).slice(0, 16)), 'requests.jsonl');
}
function readEvents(file) {
  if (!fs.existsSync(file)) return [];
  const text = fs.readFileSync(file, 'utf8');
  if (text && !text.endsWith('\n')) problem('request/journal', 'Incomplete journal tail; preserve it for recovery');
  let events;
  try { events = text.split('\n').filter(Boolean).map(line => JSON.parse(line)); }
  catch { problem('request/journal', 'Invalid journal; no writes allowed'); }
  let previous = '';
  events.forEach((event, i) => {
    const { hash, ...record } = event;
    if (event.sequence !== i + 1 || event.previous !== previous || hash !== digest(JSON.stringify(record))) problem('request/journal', 'Journal integrity check failed');
    previous = hash;
  });
  return events;
}
function requestsFrom(events) {
  const requests = new Map();
  for (const event of events) requests.set(event.request.id, event.request);
  return requests;
}
export function readRequests({ input, stateDir, repoRoot, getLoaded }) {
  const events = readEvents(journalPath(input, stateDir));
  const { revision, model } = getLoaded ? getLoaded() : loadModel(input);
  return [...requestsFrom(events).values()].map(request => {
    const receipts = request.receipts.map(receipt => ({ ...receipt, evidence: evaluateEvidence(receipt.evidence, repoRoot) }));
    return { ...request, receipts, stale: request.baseRevision !== revision || !model.entities.some(e => e.id === request.entityId) || receipts.some(r => r.evidence.some(e => e.status !== 'verified')) };
  });
}

function checkReceipt(data, request, repoRoot) {
  const ev = data.evidence || [];
  if (!Array.isArray(ev) || ev.length > 100) problem('request/evidence', 'Evidence must be a bounded array');
  for (const item of ev) {
    if (!item || !['source','test','runtime','design'].includes(item.kind) || typeof item.path !== 'string' || !/^[a-f0-9]{64}$/.test(item.sha256 || '')) problem('request/evidence', 'Evidence needs kind, path and SHA-256');
    if (['test','runtime'].includes(item.kind)) {
      if (!(item.result === 'passed' || (data.stage === 'blocked' && item.result === 'failed')) || !item.command || !item.scope || !item.capturedAt || !Array.isArray(item.inputs) || !item.inputs.length) problem('request/evidence', 'Test/runtime evidence needs result, command, scope, time and input hashes; failure can only record blocked');
      const sources = request.receipts.filter(r => r.stage === 'implemented').at(-1)?.evidence.filter(e => e.kind === 'source') || [];
      if (!sources.length || !sources.every(source => item.inputs.some(binding => binding.path === source.path && binding.sha256 === source.sha256))) problem('request/input-binding', 'Test/runtime receipt must bind every implementation source hash');
    }
    for (const binding of item.inputs || []) if (typeof binding.path !== 'string' || !/^[a-f0-9]{64}$/.test(binding.sha256 || '')) problem('request/evidence', 'Invalid input hash binding');
  }
  if (evaluateEvidence(ev, repoRoot).some(e => e.status !== 'verified')) problem('request/stale-evidence', 'Evidence is missing, unverified or changed');
  const requiredKind = { implemented: 'source', tests_passed: 'test', integrated: 'runtime' }[data.stage];
  if (requiredKind && !ev.some(e => e.kind === requiredKind)) problem('request/evidence', `${data.stage} requires ${requiredKind} evidence`);
  return ev;
}

export async function mutateRequest(options, data) {
  if (!data || typeof data !== 'object' || Array.isArray(data)) problem('request/input', 'Expected a JSON object');
  const allowed = new Set(['action','operationId','id','entityId','baseRevision','text','expectedVersion','actor','stage','note','evidence']);
  for (const key of Object.keys(data)) if (!allowed.has(key)) problem('request/input', `Unknown field ${key}`);
  identity(data.operationId, 'operationId'); identity(data.id, 'request ID');
  const file = journalPath(options.input, options.stateDir);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  const lock = `${file}.lock`; let fd;
  for (let i = 0; i < 100; i++) {
    try { fd = fs.openSync(lock, 'wx', 0o600); break; }
    catch (error) { if (error.code !== 'EEXIST') throw error; await new Promise(resolve => setTimeout(resolve, 20)); }
  }
  if (fd === undefined) problem('request/locked', 'Another writer holds the journal; retry or inspect a stale lock', {}, 409);
  try {
    fs.writeFileSync(fd, JSON.stringify({ pid: process.pid }));
    const events = readEvents(file);
    const payloadHash = digest(JSON.stringify(data));
    const duplicate = events.find(e => e.operationId === data.operationId);
    if (duplicate) {
      if (duplicate.payloadHash !== payloadHash) problem('request/idempotency-conflict', 'Operation ID was used with different data', {}, 409);
      return { ok: true, replayed: true, request: duplicate.request };
    }
    const loaded = options.getLoaded ? options.getLoaded() : loadModel(options.input);
    const requests = requestsFrom(events);
    let request = requests.get(data.id);
    if (data.baseRevision !== loaded.revision) problem('request/revision-conflict', 'The design changed. Keep this draft and review the latest revision before resubmitting.', { currentRevision: loaded.revision }, 409);
    const now = new Date().toISOString();
    if (data.action === 'create') {
      if (request) problem('request/exists', 'Request ID already exists', {}, 409);
      if (!loaded.model.entities.some(e => e.id === data.entityId)) problem('request/entity', 'Entity does not exist');
      request = { id: data.id, entityId: data.entityId, baseRevision: loaded.revision, text: bounded(data.text, 'request text', 12000), stage: 'submitted', version: 1, createdAt: now, updatedAt: now, receipts: [] };
    } else if (data.action === 'report') {
      if (!request) problem('request/missing', 'Request does not exist', {}, 404);
      if (request.version !== data.expectedVersion) problem('request/version-conflict', 'Request has changed', { currentVersion: request.version }, 409);
      if (request.baseRevision !== loaded.revision) problem('request/revision-conflict', 'Request targets an older design; explicitly rebase before reporting', { currentRevision: loaded.revision }, 409);
      bounded(data.actor, 'actor');
      const lastStage = request.stage === 'blocked' ? request.resumeStage : request.stage;
      const current = stages.indexOf(lastStage);
      const next = stages.indexOf(data.stage);
      if (data.stage !== 'blocked' && !(next === current + 1 || (data.stage === 'implemented' && current >= 2))) problem('request/transition', `Cannot move ${request.stage} to ${data.stage}`);
      if (data.stage === 'blocked') bounded(data.note, 'reason', 4000);
      if (data.stage !== 'blocked' && current >= 1) {
        const prior = request.receipts.filter(r => r.stage !== 'blocked' && !(data.stage === 'implemented' && stages.indexOf(r.stage) >= 2));
        if (prior.some(r => evaluateEvidence(r.evidence, options.repoRoot).some(e => e.status !== 'verified'))) problem('request/stale-evidence', 'Prior receipt evidence changed; record a new implementation first');
      }
      const evidence = checkReceipt(data, request, options.repoRoot);
      // A new implementation supersedes earlier validation, while old events
      // remain immutable in the journal.
      const receipts = data.stage === 'implemented' ? request.receipts.filter(r => stages.indexOf(r.stage) < 2) : request.receipts;
      request = { ...request, stage: data.stage, ...(data.stage === 'blocked' ? { resumeStage: lastStage } : {}), version: request.version + 1, updatedAt: now, receipts: [...receipts, { stage: data.stage, actor: data.actor, at: now, note: data.note || '', evidence }] };
    } else if (data.action === 'rebase') {
      if (!request || request.version !== data.expectedVersion) problem('request/version-conflict', 'Missing or changed request', {}, 409);
      if (!loaded.model.entities.some(e => e.id === request.entityId)) problem('request/entity', 'Entity no longer exists');
      bounded(data.actor, 'actor'); bounded(data.note, 'rebase reason', 4000);
      request = { ...request, baseRevision: loaded.revision, stage: 'submitted', receipts: [], version: request.version + 1, updatedAt: now, rebase: { actor: data.actor, reason: data.note } };
    } else problem('request/action', 'Expected create, report or rebase');
    const record = { sequence: events.length + 1, previous: events.at(-1)?.hash || '', operationId: data.operationId, payloadHash, request };
    const event = { ...record, hash: digest(JSON.stringify(record)) };
    const out = fs.openSync(file, 'a', 0o600);
    try { fs.writeFileSync(out, `${JSON.stringify(event)}\n`); fs.fsyncSync(out); } finally { fs.closeSync(out); }
    return { ok: true, replayed: false, request };
  } finally { fs.closeSync(fd); fs.unlinkSync(lock); }
}
