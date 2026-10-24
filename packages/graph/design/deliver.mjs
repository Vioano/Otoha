import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { loadModel, modelSnapshot, compileView, digest, problem } from './model.mjs';
import { resolveOutputPath } from '../renderers/shared/output-path.mjs';
import { otohaFilterPresets, matchesTaskFilter, filterGraphEntities } from './filters.mjs';
import { installOtohaBoard } from './board.mjs';
import { projectTasks } from './tasks.mjs';
import { mountOtohaCanvas } from './canvas-host.mjs';
import { otohaUIText, installOtohaChrome, installOtohaFrameUI } from './reader-ui.mjs';
import { viewerCatalog } from '../renderers/shared/i18n.mjs';
import { installOtohaInteractions } from './interactions.mjs';
import { otohaExpansionLayout, otohaExpandedRoute } from './nested-layout.mjs';
import { installOtohaTeam } from '../team/viewer.mjs';
import { installOtohaNodes } from './node-explorer.mjs';
import { assertProjectionCoverage, viewGraph, topologyHash } from './query.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const viewerVersion = JSON.parse(fs.readFileSync(new URL('../package.json', import.meta.url),'utf8')).version;
const safeJSON = value => JSON.stringify(value).replaceAll('<', '\\u003c').replaceAll('\u2028', '\\u2028').replaceAll('\u2029', '\\u2029');
export function explorerHTML(snapshot, views) {
  const template = fs.readFileSync(new URL('./viewer.html', import.meta.url), 'utf8');
  const renderer = fs.readFileSync(new URL('../assets/template.html', import.meta.url), 'utf8');
  const tokens = renderer.slice(renderer.indexOf('    :root,'), renderer.indexOf('    * { margin: 0;'));
  if (!tokens.includes('--frontend-fill')) problem('system/themes', 'Bundled renderer theme tokens unavailable');
  const themes = fs.readFileSync(new URL('./themes.css', import.meta.url), 'utf8');
  const viewportCSS = fs.readFileSync(new URL('./viewport.css', import.meta.url), 'utf8');
  const chromeCSS=fs.readFileSync(new URL('./chrome.css',import.meta.url),'utf8');
  const boardCSS=fs.readFileSync(new URL('./board.css',import.meta.url),'utf8');
  const uiMessages=JSON.parse(fs.readFileSync(new URL('./ui.en.json',import.meta.url),'utf8'));
  const rendererCatalogs={en:viewerCatalog('en'),'zh-CN':viewerCatalog('zh-CN')};
  return template.replace('__ARCHIFY_SYSTEM_DATA__', () => safeJSON({ snapshot, views, themes, viewportCSS, chromeCSS, uiMessages, rendererCatalogs, viewerVersion }))
    .replace('__OTOHA_BRAND_MARK__', () => fs.readFileSync(new URL('../assets/otoha-mark.svg', import.meta.url), 'utf8'))
    .replace('__OTOHA_FAVICON__', () => 'data:image/svg+xml,' + encodeURIComponent(fs.readFileSync(new URL('../assets/otoha-icon.svg', import.meta.url), 'utf8')))
    .replace('__SYSTEM_OTOHA_THEME_TOKENS__', () => tokens + themes + chromeCSS + boardCSS)
    .replace('__SYSTEM_OTOHA_INTERACTIONS__', () => [mountOtohaCanvas, otohaUIText, installOtohaChrome, installOtohaFrameUI, installOtohaInteractions, otohaExpansionLayout, otohaExpandedRoute, installOtohaNodes, installOtohaTeam, otohaFilterPresets, matchesTaskFilter, filterGraphEntities, projectTasks, installOtohaBoard].map(fn=>fn.toString()).join('\n'));
}
export function buildDesign(input, options = {}) {
  const loaded = options.loaded || loadModel(input);
  assertProjectionCoverage(loaded.model);
  const staging = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-system-build-'));
  try {
    const views = {}; const receipts = [];
    for (const view of loaded.model.views) {
      const spec = compileView(loaded.model, view);
      const graph = viewGraph(loaded.model, view.id);
      if (topologyHash({entities:graph.entities.map(e=>({id:e.id})),relations:graph.relations}) !== topologyHash({entities:spec.components,relations:spec.connections.map(e=>({...e,kind:graph.relations.find(r=>r.id===e.id).kind}))})) problem('graph/projection', 'Rendered topology differs from the shared graph projection', {view:view.id});
      const specPath = path.join(staging, `${view.id}.json`);
      const output = path.join(staging, `${view.id}.html`);
      fs.writeFileSync(specPath, JSON.stringify(spec));
      const run = spawnSync(process.execPath, [path.join(root, 'bin/archify.mjs'), 'deliver', 'architecture', specPath, output, '--quality', 'showcase', '--json'], { encoding: 'utf8', timeout: 30000, maxBuffer: 4 * 1024 * 1024 });
      let receipt; try { receipt = JSON.parse(run.stdout); } catch {}
      if (run.status !== 0 || !receipt?.ok) problem('system/view-delivery', `View ${view.id} did not pass delivery`, { view: view.id, receipt, error: run.error?.message });
      views[view.id] = fs.readFileSync(output, 'utf8');
      receipts.push({ view: view.id, specification: { sha256: receipt.specification.sha256, bytes: receipt.specification.bytes }, artifact: { sha256: receipt.artifact.sha256, bytes: receipt.artifact.bytes }, validation: receipt.validation });
    }
    const snapshot = modelSnapshot(loaded, options.repoRoot);
    const html = explorerHTML(snapshot, views);
    return { loaded, snapshot, views, html, receipt: { ok: true, viewerVersion, extension: 'system-design-v1', specification: { sha256: loaded.revision, bytes: loaded.bytes.length }, artifact: { sha256: digest(html), bytes: Buffer.byteLength(html) }, views: receipts, evidence: snapshot.model.evidence.map(e => ({ id: e.id, status: e.status })), visualReview: 'pending' } };
  } finally { fs.rmSync(staging, { recursive: true, force: true }); }
}
export function deliverDesign(input, output, options = {}) {
  const outputRequest = { requestedOutput: output, defaultOutput: 'system.html', inputPaths: [path.resolve(input)], inputDescription: 'its system JSON input', cwd: process.cwd() };
  const { outputPath } = resolveOutputPath(outputRequest);
  if (!/\.html?$/i.test(outputPath)) problem('system/output', 'Explorer output must use .html');
  const built = buildDesign(input, options);
  fs.mkdirSync(path.dirname(outputPath), { recursive: true });
  const staging = fs.mkdtempSync(path.join(path.dirname(outputPath), '.archify-system-delivery-'));
  try {
    const candidate = path.join(staging, 'system.html');
    fs.writeFileSync(candidate, built.html);
    resolveOutputPath(outputRequest);
    fs.renameSync(candidate, outputPath);
  } finally { fs.rmSync(staging, { recursive: true, force: true }); }
  return { ...built, receipt: { ...built.receipt, output: outputPath } };
}
