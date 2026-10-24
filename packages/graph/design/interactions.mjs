// Installed into each embedded view. Uses the existing camera, radar and reset
// controls so gestures cannot create a second, unsynchronised camera state.
export function installOtohaInteractions(win) {
  const doc = win.document;
  const panel = doc.querySelector('.diagram-container');
  const svg = panel?.querySelector(':scope > svg');
  const camera = win.Archify?.view;
  if (!svg || !camera) return;
  // One continuous grid follows the existing camera, including letterboxed space.
  const grid = () => {
    const matrix=svg.getScreenCTM(),bounds=panel.getBoundingClientRect();if(!matrix)return;
    panel.style.setProperty('--otoha-grid-size',`${40*matrix.a}px ${40*matrix.d}px`);
    panel.style.setProperty('--otoha-grid-position',`${matrix.e-bounds.left}px ${matrix.f-bounds.top}px`);
  };
  const gridObserver=new win.MutationObserver(grid);gridObserver.observe(svg,{attributes:true,attributeFilter:['viewBox','style']});
  const gridResize=new win.ResizeObserver(grid);gridResize.observe(panel);win.requestAnimationFrame(grid);
  win.addEventListener('pagehide',()=>{gridObserver.disconnect();gridResize.disconnect();},{once:true});
  const controls = '.diagram-nav, .focus-chip, .node-finder, .diagram-guide, .overview-map, .route-probe, .semantic-lens, input, textarea, select, [contenteditable]';
  let timer, gestureScale = null;
  const finish = () => { panel.classList.remove('otoha-gesturing'); };
  const gestureEnd = () => { gestureScale = null; finish(); };
  const moving = () => {
    panel.classList.add('otoha-gesturing');
    win.clearTimeout(timer);
    timer = win.setTimeout(finish, 160);
  };
  const logicalPoint = (clientX, clientY) => {
    const matrix = svg.getScreenCTM();
    if (!matrix) return null;
    const point = svg.createSVGPoint();
    point.x = clientX; point.y = clientY;
    return point.matrixTransform(matrix.inverse());
  };
  const centerPoint = () => {
    const state = camera.state(), rect = svg.getBoundingClientRect();
    return logicalPoint(rect.left - state.x + svg.clientWidth / 2,
      rect.top - state.y + svg.clientHeight / 2);
  };
  const zoom = (next, clientX, clientY) => {
    const before = camera.state(), center = centerPoint();
    const anchor = logicalPoint(clientX, clientY);
    if (!center || !anchor || !Number.isFinite(next)) return;
    next = Math.max(1, Math.min(3, next));
    const ratio = before.scale / next;
    // Keep the world point under the pointer fixed unless the camera hits an edge.
    const x = anchor.x + (center.x - anchor.x) * ratio;
    const y = anchor.y + (center.y - anchor.y) * ratio;
    moving();
    camera.centerAt(x, y, { scale: next, instant: true });
  };
  const wheel = event => {
    if(win.otohaAnnotationActive?.()||doc.querySelector('style[data-browser-comment-cursor-style]'))return;
    if (event.target.closest?.(controls)) return;
    const before = camera.state(), center = centerPoint();
    const matrix = svg.getScreenCTM();
    if (!center || !matrix) return;
    event.preventDefault();
    if (gestureScale !== null) return;
    const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? panel.clientHeight : 1;
    if (event.ctrlKey || event.metaKey) {
      zoom(before.scale * Math.exp(-event.deltaY * unit * 0.012), event.clientX, event.clientY);
    } else {
      moving();
      camera.centerAt(center.x + event.deltaX * unit / matrix.a,
        center.y + event.deltaY * unit / matrix.d,
        { scale: before.scale, instant: true });
    }
  };
  const gestureStart = event => {
    if(win.otohaAnnotationActive?.()||doc.querySelector('style[data-browser-comment-cursor-style]'))return;
    if (event.target.closest?.(controls)) return;
    event.preventDefault();
    gestureScale = camera.state().scale;
  };
  const gestureChange = event => {
    if (gestureScale === null) return;
    event.preventDefault();
    const initial = gestureScale;
    zoom(initial * event.scale, event.clientX, event.clientY);
    gestureScale = initial;
  };
  panel.addEventListener('wheel', wheel, { passive: false });
  panel.addEventListener('gesturestart', gestureStart, { passive: false });
  panel.addEventListener('gesturechange', gestureChange, { passive: false });
  panel.addEventListener('gestureend', gestureEnd);
  win.addEventListener('pagehide', () => {
    win.clearTimeout(timer);
    panel.removeEventListener('wheel', wheel);
    panel.removeEventListener('gesturestart', gestureStart);
    panel.removeEventListener('gesturechange', gestureChange);
    panel.removeEventListener('gestureend', gestureEnd);
  }, { once: true });
}
