import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFileSync} from 'node:fs';

const script = readFileSync(new URL('../skills/otoha-visualize/assets/view-state.js', import.meta.url), 'utf8');
const plain = value => JSON.parse(JSON.stringify(value));
const tick = () => new Promise(resolve => setImmediate(resolve));

function harness({standalone = false, loading = false} = {}) {
  const sent = [], order = [], listeners = new Map(), domListeners = new Map(), frames = [];
  const element = (id, tagName = 'DETAILS') => ({
    id, tagName, open: false,
    focus(options) { order.push(['focus', id, plain(options)]); },
    get value() { throw new Error('must not read form value'); },
    set value(_) { throw new Error('must not write form value'); },
    get textContent() { throw new Error('must not read text'); }
  });
  const detail = element('stable-detail'), input = element('stable-input', 'INPUT');
  const nodes = new Map([[detail.id, detail], [input.id, input]]);
  const parent = {postMessage(message, origin) { sent.push([plain(message), origin]); }};
  const document = {
    readyState: loading ? 'loading' : 'complete',
    documentElement: {dataset: {theme: 'dark'}},
    fonts: {ready: Promise.resolve()}, activeElement: input,
    querySelectorAll(selector) { assert.equal(selector, 'details[id]'); return [detail]; },
    getElementById(id) { return nodes.get(id) || null; },
    addEventListener(type, fn) { domListeners.set(type, fn); },
    removeEventListener(type, fn) { if (domListeners.get(type) === fn) domListeners.delete(type); }
  };
  const window = {
    parent, console, scrollX: 3, scrollY: 410,
    requestAnimationFrame(fn) { frames.push(fn); },
    scrollTo(x, y) { order.push(['scroll', x, y]); },
    addEventListener(type, fn) { listeners.set(type, fn); },
    removeEventListener(type, fn) { if (listeners.get(type) === fn) listeners.delete(type); },
    get localStorage() { throw new Error('must not access storage'); },
    get sessionStorage() { throw new Error('must not access storage'); }
  };
  if (standalone) window.parent = window;
  vm.runInNewContext(script, {window, document});
  const message = (data, source = parent) => listeners.get('message')?.({source, data});
  async function settle() {
    for (let i = 0; i < 4; i++) {
      await tick();
      for (const fn of frames.splice(0)) fn();
    }
    await tick();
  }
  return {window, document, detail, input, nodes, parent, sent, order, frames, listeners, domListeners, message, settle};
}

function state(extra = {}) {
  return {theme: 'light', details: [{id: 'stable-detail', open: true}], scroll: {x: 7, y: 900}, focus_id: 'stable-input', ...extra};
}

test('ready waits for DOM and rendered frames; capture returns only whitelisted state', async () => {
  const h = harness({loading: true});
  h.window.OtohaViewState.install({readViewId: () => 'plan'});
  await h.settle();
  assert.equal(h.sent.length, 0);
  h.domListeners.get('DOMContentLoaded')();
  await h.settle();
  assert.deepEqual(h.sent.shift(), [{type: 'otoha:view-state:ready', version: 1}, '*']);
  h.message({type: 'otoha:view-state:capture', version: 1, request_id: 'capture-1', value: 'private'});
  assert.deepEqual(h.sent.shift(), [{type: 'otoha:view-state:snapshot', version: 1, request_id: 'capture-1', state: {
    theme: 'dark', details: [{id: 'stable-detail', open: false}], scroll: {x: 3, y: 410}, focus_id: 'stable-input', view_id: 'plan'
  }}, '*']);
});

test('ignores other windows, unsupported versions, malformed requests and invalid states', async () => {
  const h = harness();
  h.window.OtohaViewState.install();
  await h.settle(); h.sent.length = 0;
  h.message({type: 'otoha:view-state:capture', version: 1, request_id: 'x'}, {});
  h.message({type: 'otoha:view-state:capture', version: 2, request_id: 'x'});
  h.message({type: 'otoha:view-state:capture', version: 1, request_id: {secret: true}});
  for (const invalid of [state({theme: 'sepia'}), state({scroll: {x: NaN, y: 0}}), state({details: [{id: 'stable-detail', open: true}, {id: 'stable-detail', open: false}]}), state({focus_id: {value: 'private'}})]) {
    h.message({type: 'otoha:view-state:restore', version: 1, state: invalid});
  }
  await h.settle();
  assert.equal(h.sent.length, 0);
  assert.deepEqual(h.order, []);
  assert.equal(h.document.documentElement.dataset.theme, 'dark');
});

test('restores view before theme/details, layout before scroll, focus without scrolling', async () => {
  const h = harness();
  let releaseView;
  h.window.OtohaViewState.install({
    applyViewId: id => { h.order.push(['view', id]); return new Promise(resolve => { releaseView = resolve; }); },
    applyTheme: theme => { h.order.push(['theme', theme]); h.document.documentElement.dataset.theme = theme; }
  });
  await h.settle();
  const restored = state({view_id: 'plan', value: 'secret', html: '<input>', arbitrary: {private: true}});
  restored.details.push({id: 'missing', open: true}, {id: 'stable-input', open: true});
  h.message({type: 'otoha:view-state:restore', version: 1, state: restored});
  await tick();
  assert.deepEqual(h.order, [['view', 'plan']]);
  assert.equal(h.detail.open, false);
  releaseView(); await tick();
  assert.equal(h.detail.open, true);
  assert.equal(h.input.open, false);
  assert.deepEqual(h.order, [['view', 'plan'], ['theme', 'light']]);
  await h.settle();
  assert.deepEqual(h.order, [['view', 'plan'], ['theme', 'light'], ['scroll', 7, 900], ['focus', 'stable-input', {preventScroll: true}]]);
});

test('theme hooks reuse a page theme mechanism; missing IDs are ignored', async () => {
  const h = harness(); let pageTheme = 'light';
  h.document.documentElement.dataset = {};
  h.window.OtohaViewState.install({readTheme: () => pageTheme, applyTheme: theme => { pageTheme = theme; }});
  await h.settle(); h.sent.length = 0;
  h.message({type: 'otoha:view-state:capture', version: 1, request_id: 'theme'});
  assert.equal(h.sent[0][0].state.theme, 'light');
  h.message({type: 'otoha:view-state:restore', version: 1, state: state({theme: 'dark', focus_id: 'removed', details: [{id: 'removed', open: true}]})});
  await h.settle();
  assert.equal(pageTheme, 'dark');
  assert.deepEqual(h.order, [['scroll', 7, 900]]);
});

test('unidentified details are omitted and invalid theme never becomes a fabricated snapshot', async () => {
  const h = harness();
  h.window.OtohaViewState.install();
  await h.settle(); h.sent.length = 0;
  h.document.querySelectorAll = () => [h.detail, {id: '', open: true}];
  h.message({type: 'otoha:view-state:capture', version: 1, request_id: 'empty-id'});
  assert.deepEqual(h.sent[0][0].state.details, [{id: 'stable-detail', open: false}]);
  delete h.document.documentElement.dataset.theme;
  h.message({type: 'otoha:view-state:capture', version: 1, request_id: 'invalid-theme'});
  assert.equal(h.sent.length, 1);
});

test('dispose removes listeners and prevents pending ready/restore work', async () => {
  const h = harness();
  const dispose = h.window.OtohaViewState.install();
  h.message({type: 'otoha:view-state:restore', version: 1, state: state()});
  dispose(); await h.settle();
  assert.equal(h.listeners.size, 0);
  assert.equal(h.domListeners.size, 0);
  assert.equal(h.sent.length, 0);
  assert.deepEqual(h.order, []);
});

test('standalone page installs no listener, sends nothing, never uses storage or callbacks', async () => {
  const h = harness({standalone: true});
  const dispose = h.window.OtohaViewState.install({readViewId: () => { throw new Error('not called'); }});
  assert.equal(typeof dispose, 'function'); dispose();
  await h.settle();
  assert.equal(h.listeners.size, 0);
  assert.equal(h.domListeners.size, 0);
  assert.equal(h.sent.length, 0);
});
