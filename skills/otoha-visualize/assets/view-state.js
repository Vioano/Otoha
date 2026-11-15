/* Otoha opaque-frame reading state v1. Inline in the final rendered document. */
(function () {
  'use strict';
  const validID = id => typeof id === 'string' && id.length > 0 && id.length <= 256;

  function normalize(value) {
    if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
    if (value.theme !== 'dark' && value.theme !== 'light') return null;
    if (!Array.isArray(value.details) || value.details.length > 1000) return null;
    if (!value.scroll || !Number.isFinite(value.scroll.x) || !Number.isFinite(value.scroll.y) || value.scroll.x < 0 || value.scroll.y < 0) return null;
    if (value.focus_id !== null && !validID(value.focus_id)) return null;
    if (value.view_id !== undefined && value.view_id !== null && !validID(value.view_id)) return null;
    const ids = new Set(), details = [];
    for (const item of value.details) {
      if (!item || !validID(item.id) || typeof item.open !== 'boolean' || ids.has(item.id)) return null;
      ids.add(item.id);
      details.push({id: item.id, open: item.open});
    }
    const state = {theme: value.theme, details, scroll: {x: value.scroll.x, y: value.scroll.y}, focus_id: value.focus_id};
    if (value.view_id !== undefined) state.view_id = value.view_id;
    return state;
  }

  function install(options = {}) {
    if (window.parent === window) return function dispose() {};
    const parent = window.parent;
    let disposed = false;
    const readTheme = options.readTheme || (() => document.documentElement.dataset.theme);
    const applyTheme = options.applyTheme || (theme => { document.documentElement.dataset.theme = theme; });
    const frame = () => new Promise(resolve => window.requestAnimationFrame(resolve));
    async function settled() {
      if (document.fonts && document.fonts.ready) await document.fonts.ready;
      await frame();
      await frame();
    }
    function post(message) {
      if (!disposed) parent.postMessage({...message, version: 1}, '*');
    }
    function snapshot() {
      const state = {
        theme: readTheme(),
        details: Array.from(document.querySelectorAll('details[id]')).filter(element => validID(element.id))
          .map(element => ({id: element.id, open: element.open})),
        scroll: {x: window.scrollX, y: window.scrollY},
        focus_id: document.activeElement && validID(document.activeElement.id) ? document.activeElement.id : null
      };
      if (options.readViewId) state.view_id = options.readViewId();
      return normalize(state);
    }
    async function restore(state) {
      if (options.applyViewId && state.view_id !== undefined) await options.applyViewId(state.view_id);
      if (disposed) return;
      applyTheme(state.theme);
      for (const item of state.details) {
        const element = document.getElementById(item.id);
        if (element && element.tagName === 'DETAILS') element.open = item.open;
      }
      await settled();
      if (disposed) return;
      window.scrollTo(state.scroll.x, state.scroll.y);
      const focus = state.focus_id === null ? null : document.getElementById(state.focus_id);
      if (focus && typeof focus.focus === 'function') focus.focus({preventScroll: true});
    }
    function onMessage(event) {
      if (disposed || event.source !== parent) return;
      const message = event.data;
      if (!message || typeof message !== 'object' || Array.isArray(message) || message.version !== 1) return;
      if (message.type === 'otoha:view-state:capture' && validID(message.request_id)) {
        const state = snapshot();
        if (state) post({type: 'otoha:view-state:snapshot', request_id: message.request_id, state});
      } else if (message.type === 'otoha:view-state:restore') {
        const state = normalize(message.state);
        if (state) void restore(state).catch(error => window.console.error('Otoha view-state restore failed', error));
      }
    }
    async function ready() {
      await settled();
      post({type: 'otoha:view-state:ready'});
    }
    function onReady() { void ready().catch(error => window.console.error('Otoha view-state ready failed', error)); }
    window.addEventListener('message', onMessage);
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', onReady, {once: true});
    else onReady();
    return function dispose() {
      disposed = true;
      window.removeEventListener('message', onMessage);
      document.removeEventListener('DOMContentLoaded', onReady);
    };
  }

  window.OtohaViewState = Object.freeze({install});
})();
