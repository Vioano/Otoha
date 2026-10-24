import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';

const schema = JSON.parse(fs.readFileSync(new URL('../schemas/system.schema.json', import.meta.url), 'utf8'));
export const digest = value => createHash('sha256').update(value).digest('hex');
export const axes = ['design', 'implementation', 'verification', 'integration'];
export class DesignError extends Error {
  constructor(code, message, details = {}, status = 400) {
    super(message); Object.assign(this, { code, details, status });
  }
}
export function problem(code, message, details, status) { throw new DesignError(code, message, details, status); }

// Small, dependency-free interpreter for the explicitly used schema vocabulary.
// The schema is bundled and trusted; unknown validation keywords fail at startup.
const vocabulary = new Set(['$schema', '$defs', '$ref', 'title', 'type', 'const', 'enum', 'properties', 'required', 'additionalProperties', 'items', 'minItems', 'maxItems', 'uniqueItems', 'minLength', 'maxLength', 'minimum', 'pattern']);
function checkSchema(value, rule, location, issues) {
  for (const key of Object.keys(rule)) if (!vocabulary.has(key)) throw new Error(`Unsupported system schema keyword: ${key}`);
  if (rule.$ref) return checkSchema(value, schema.$defs[rule.$ref.split('/').at(-1)], location, issues);
  const fail = message => issues.push({ code: 'system/schema', subject: location, message });
  if ('const' in rule && value !== rule.const) fail(`Expected ${JSON.stringify(rule.const)}`);
  if (rule.enum && !rule.enum.includes(value)) fail(`Expected one of ${rule.enum.join(', ')}`);
  if (rule.type) {
    const valid = rule.type === 'array' ? Array.isArray(value) : rule.type === 'object' ? value !== null && typeof value === 'object' && !Array.isArray(value) : rule.type === 'integer' ? Number.isInteger(value) : rule.type === 'number' ? typeof value === 'number' && Number.isFinite(value) : typeof value === rule.type;
    if (!valid) { fail(`Expected ${rule.type}`); return; }
  }
  if (rule.type === 'object') {
    for (const key of rule.required || []) if (!(key in value)) fail(`Missing ${key}`);
    for (const [key, child] of Object.entries(value)) {
      if (!Object.hasOwn(rule.properties || {}, key)) { if (rule.additionalProperties === false) fail(`Unknown property ${key}`); }
      else checkSchema(child, rule.properties[key], `${location}/${key}`, issues);
    }
  }
  if (Array.isArray(value)) {
    if (value.length < (rule.minItems ?? 0) || value.length > (rule.maxItems ?? Infinity)) fail('Array size outside contract');
    if (rule.uniqueItems && new Set(value.map(x => JSON.stringify(x))).size !== value.length) fail('Duplicate array items');
    if (rule.items) value.forEach((x, i) => checkSchema(x, rule.items, `${location}/${i}`, issues));
  }
  if (typeof value === 'string') {
    if (value.length < (rule.minLength ?? 0) || value.length > (rule.maxLength ?? Infinity)) fail('Text length outside contract');
    if (rule.pattern && !new RegExp(rule.pattern).test(value)) fail('Text does not match required format');
  }
  if (typeof value === 'number' && value < (rule.minimum ?? -Infinity)) fail('Number below minimum');
}

export function validateModel(model) {
  const issues = [];
  checkSchema(model, schema, '', issues);
  if (issues.length) return issues;
  const err = (code, subject, message) => issues.push({ code: `system/${code}`, subject, message });
  const maps = {};
  for (const key of ['entities', 'relations', 'views', 'evidence', 'tasks']) {
    maps[key] = new Map();
    for (const item of model[key] || []) {
      if (maps[key].has(item.id)) err('duplicate-id', item.id, `Duplicate ${key} identity`);
      maps[key].set(item.id, item);
    }
  }
  const { entities, relations, views, evidence } = maps;
  if (!views.has(model.meta.rootView) || views.get(model.meta.rootView)?.scope) err('root-view', model.meta.rootView, 'Root view must exist and be unscoped');
  const crosscut = v => ['perspective', 'scenario'].includes(v.kind);
  if (crosscut(views.get(model.meta.rootView) || {})) err('root-view', model.meta.rootView, 'Root must be a structural view');
  if (model.views.filter(v => !v.scope && !crosscut(v)).length !== 1) err('root-view', 'views', 'Exactly one unscoped structural root view is required');
  const scoped = new Map();
  const projected = new Set();
  for (const view of model.views) {
    if (crosscut(view) && view.scope) err('perspective-scope', view.id, 'Cross-cutting views do not establish containment');
    for (const id of view.entryPoints || []) if (!entities.has(id)) err('unknown-entry', view.id, id);
    if (view.scope) {
      if (!entities.has(view.scope)) err('unknown-scope', view.id, 'Unknown containing entity');
      if (scoped.has(view.scope)) err('duplicate-scope', view.id, 'An entity has only one internal view');
      scoped.set(view.scope, view.id);
    }
    const ids = new Set();
    for (const p of view.placements) {
      if (!entities.has(p.entity)) err('unknown-entity', view.id, `Unknown ${p.entity}`);
      if (ids.has(p.entity)) err('duplicate-placement', view.id, `Duplicate ${p.entity}`);
      ids.add(p.entity); projected.add(p.entity);
      if (p.size && p.size.some(n => n <= 0)) err('placement-size', view.id, 'Sizes must be positive');
    }
    for (const chapter of view.guidedViews || []) for (const id of chapter.focus) if (!ids.has(id)) err('chapter-focus', view.id, `Chapter refers to an entity outside this view: ${id}`);
    const shownRelations = new Set();
    for (const route of view.relations) {
      const relation = relations.get(route.relation);
      if (!relation || !ids.has(relation.from) || !ids.has(relation.to)) err('view-relation', view.id, `Relation ${route.relation} must have both endpoints in this view`);
      if (shownRelations.has(route.relation)) err('duplicate-relation', view.id, route.relation);
      shownRelations.add(route.relation);
    }
  }
  for (const entity of model.entities) {
    const seen = new Set([entity.id]); let current = entity;
    while (current.parent) {
      if (!entities.has(current.parent)) { err('unknown-parent', entity.id, current.parent); break; }
      if (seen.has(current.parent)) { err('containment-cycle', entity.id, 'Containment must be acyclic'); break; }
      seen.add(current.parent); current = entities.get(current.parent);
    }
    const home = entity.parent ? views.get(scoped.get(entity.parent)) : views.get(model.meta.rootView);
    const crosscutHome = model.views.some(v => crosscut(v) && (!entity.parent || v.entryPoints?.includes(entity.parent)) && v.placements.some(p => p.entity === entity.id));
    if (!home?.placements.some(p => p.entity === entity.id) && !crosscutHome) err('unreachable-entity', entity.id, 'Entity must appear in its parent internal view, root, or an explicit cross-cutting view linked to its parent');
    for (const id of entity.evidence) if (!evidence.has(id)) err('unknown-evidence', entity.id, id);
    for (const axis of axes) {
      const claim = entity.maturity[axis];
      const refs = (claim.evidence || []).map(id => evidence.get(id));
      for (const id of claim.evidence || []) if (!entity.evidence.includes(id)) err('unbound-evidence', entity.id, `${axis}: ${id}`);
      const positive = { design: ['accepted'], implementation: ['partial','implemented'], verification: ['partial','passed','failed'], integration: ['connected','running_verified'] }[axis].includes(claim.value);
      const kind = { design: 'design', implementation: 'source', verification: 'test', integration: 'runtime' }[axis];
      if (positive && !refs.some(e => e?.kind === kind)) err('unsupported-claim', entity.id, `${axis}=${claim.value} requires ${kind} evidence`);
      if (axis === 'verification' && ['passed','failed'].includes(claim.value) && !refs.some(e => e?.kind === 'test' && e.result === claim.value)) err('test-result', entity.id, 'Claim must match test result');
      if (axis === 'integration' && positive && !refs.some(e => e?.kind === 'runtime' && e.result === 'passed')) err('runtime-result', entity.id, 'Integration requires passing scoped runtime evidence');
      if (positive && ['verification', 'integration'].includes(axis)) {
        const sources = (entity.maturity.implementation.evidence || []).map(id => evidence.get(id)).filter(e => e?.kind === 'source');
        const checks = refs.filter(e => e?.kind === kind);
        if (!sources.length || checks.some(check => !sources.some(source => check.inputs?.some(binding => binding.path === source.path && binding.sha256 === source.sha256)))) err('evidence-input-binding', entity.id, `${axis} evidence must bind this entity's declared implementation sources`);
        if (claim.value !== 'partial' && !sources.every(source => checks.some(check => check.inputs?.some(binding => binding.path === source.path && binding.sha256 === source.sha256)))) err('evidence-input-binding', entity.id, `${axis} evidence does not cover all declared implementation source hashes`);
        if ((claim.value === 'passed' || axis === 'integration') && checks.some(check => check.result !== 'passed')) err('evidence-result-conflict', entity.id, `${axis} has a conflicting failed result`);
      }
    }
  }
  for (const task of model.tasks || []) for (const id of task.entities) if (!entities.has(id)) err('task-target', task.id, `Unknown module ${id}`);
  for (const relation of model.relations) if (!entities.has(relation.from) || !entities.has(relation.to)) err('relation-endpoint', relation.id, 'Unknown endpoint');
  for (const ev of model.evidence) {
    if (ev.end_line && ev.end_line < (ev.line || 1)) err('source-range', ev.id, 'End line precedes start');
    if (['test','runtime'].includes(ev.kind) && (!ev.inputs?.length || !ev.scope || !ev.result || !ev.capturedAt || !ev.command)) err('evidence-contract', ev.id, 'Test/runtime evidence needs scope, command, result, capturedAt and exact input hashes');
  }
  return issues;
}

export function parseModel(bytes) {
  bytes = Buffer.from(bytes);
  if (bytes.length > 8 * 1024 * 1024) problem('system/input-size', 'System model exceeds 8 MiB');
  let model;
  try { model = JSON.parse(bytes); } catch (error) { problem('system/json', error.message); }
  const issues = validateModel(model);
  if (issues.length) problem('system/invalid', 'System model failed validation', { diagnostics: issues });
  return { model, revision: digest(bytes), bytes };
}

export function loadModel(input) { return parseModel(fs.readFileSync(input)); }

export function resolveBoundFile(repoRoot, relative) {
  if (!repoRoot) problem('evidence/no-root', 'Evidence root was not supplied');
  if (path.isAbsolute(relative) || relative.split(/[\\/]/).includes('..')) problem('evidence/path', 'Evidence path must stay within its repository', { path: relative });
  const root = fs.realpathSync(repoRoot); const file = fs.realpathSync(path.join(root, relative));
  const rel = path.relative(root, file);
  if (rel.startsWith('..') || path.isAbsolute(rel)) problem('evidence/path', 'Evidence symlink leaves repository', { path: relative });
  const st = fs.statSync(file);
  if (!st.isFile() || st.size > 64 * 1024 * 1024) problem('evidence/file', 'Evidence must be a regular file up to 64 MiB', { path: relative });
  return file;
}
export function evaluateBinding(binding, repoRoot) {
  if (!repoRoot) return { status: 'unverified', path: binding.path };
  try {
    const actual = digest(fs.readFileSync(resolveBoundFile(repoRoot, binding.path)));
    return { status: actual === binding.sha256 ? 'verified' : 'stale', path: binding.path, expected: binding.sha256, actual };
  } catch (error) { return { status: 'unavailable', path: binding.path, reason: error.code || error.message }; }
}
export function evaluateEvidence(evidence, repoRoot) {
  return evidence.map(ev => {
    const checks = [evaluateBinding(ev, repoRoot), ...(ev.inputs || []).map(x => evaluateBinding(x, repoRoot))];
    return { ...ev, checks, status: checks.every(x => x.status === 'verified') ? 'verified' : checks.some(x => x.status === 'stale') ? 'stale' : checks.some(x => x.status === 'unavailable') ? 'unavailable' : 'unverified' };
  });
}
export function modelSnapshot(loaded, repoRoot) {
  const evidence = evaluateEvidence(loaded.model.evidence, repoRoot);
  const byId = new Map(evidence.map(e => [e.id, e]));
  const entities = loaded.model.entities.map(entity => ({ ...entity, maturity: Object.fromEntries(axes.map(axis => {
    const claim = entity.maturity[axis]; const refs = (claim.evidence || []).map(id => byId.get(id));
    const current = refs.every(e => e?.status === 'verified');
    return [axis, { ...claim, effective: current ? claim.value : 'unknown', evidenceStatus: current ? (refs.length ? 'verified' : 'not_required') : refs.some(e => e?.status === 'stale') ? 'stale' : 'unverified' }];
  })) }));
  return { model: { ...loaded.model, entities, evidence }, revision: loaded.revision, evidenceRevision: digest(JSON.stringify(evidence)), observedAt: new Date().toISOString() };
}

export function compileView(model, view) {
  const entities = new Map(model.entities.map(e => [e.id, e]));
  const relations = new Map(model.relations.map(e => [e.id, e]));
  return {
    schema_version: 1, diagram_type: 'architecture',
    meta: { title: view.title, locale: model.meta.locale, quality_profile: 'showcase', ...(view.viewBox ? { viewBox: view.viewBox } : {}), ...(view.guidedViews ? { views: view.guidedViews } : {}) },
    components: view.placements.map(p => {
      const e = entities.get(p.entity);
      return { id: e.id, type: p.type ?? e.type, label: p.label ?? e.label, ...((p.sublabel ?? e.sublabel) ? { sublabel: p.sublabel ?? e.sublabel } : {}), ...(p.tag ? { tag: p.tag } : {}), pos: p.pos, size: p.size || [170, 76] };
    }),
    connections: view.relations.map(({ relation, ...geometry }) => {
      const r = relations.get(relation);
      return { id: r.id, from: r.from, to: r.to, label: r.label, variant: r.kind === 'dependency' ? 'dashed' : r.kind === 'feedback' ? 'emphasis' : 'default', ...geometry };
    }),
    ...(view.cards ? { cards: view.cards } : {}),
  };
}
