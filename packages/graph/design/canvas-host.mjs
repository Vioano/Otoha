// Mount the bundled renderer into one open shadow tree, not a child document.
// This keeps browser annotation hit-testing and pointer events in the host page
// while isolating the renderer's styles, element IDs, camera and lifecycle.
export function mountOtohaCanvas(host, html) {
  const page=host.ownerDocument, real=page.defaultView;
  const shadow=host.shadowRoot||host.attachShadow({mode:'open'});
  const parsed=new real.DOMParser().parseFromString(html,'text/html');
  const scripts=[...parsed.querySelectorAll('script:not([type]),script[type="text/javascript"]')].map(el=>{const code=el.textContent;el.remove();return code;});
  const root=page.importNode(parsed.documentElement,true);
  const base=page.createElement('style');base.textContent=':host{display:block;position:relative;contain:layout paint style;width:100%;height:100%;min-width:0;min-height:0}html{display:block}head{display:none}';
  shadow.replaceChildren(base,root);
  const head=root.querySelector('head'),body=root.querySelector('body');
  const mediaCleanup=[];
  const abort=new real.AbortController(),timers=new Set(),intervals=new Set(),frames=new Set(),observers=new Set(),events=new real.EventTarget();
  let disposed=false,rendererURL=new URL(page.location.href);rendererURL.hash='';
  const options=value=>typeof value==='boolean'?{capture:value,signal:abort.signal}:{...value,signal:abort.signal};
  const documentProxy=new Proxy(page,{get(target,key){
    if(key==='documentElement')return root;if(key==='body')return body;if(key==='head')return head;
    if(key==='activeElement')return shadow.activeElement;if(key==='defaultView')return win;
    if(key==='title')return root.querySelector('title')?.textContent||'';
    if(key==='styleSheets')return [...shadow.querySelectorAll('style,link[rel=stylesheet]')].map(el=>el.sheet).filter(Boolean);
    if(key==='getElementById')return id=>shadow.querySelector('#'+real.CSS.escape(id));
    if(key==='querySelector'||key==='querySelectorAll'||key==='elementFromPoint')return shadow[key].bind(shadow);
    if(key==='addEventListener')return (type,fn,opts)=>{const target=type==='visibilitychange'?page:shadow;target.addEventListener(type,fn,options(opts));};
    if(key==='removeEventListener')return (type,fn,opts)=>(type==='visibilitychange'?page:shadow).removeEventListener(type,fn,opts);
    const value=Reflect.get(target,key,target);return typeof value==='function'?value.bind(target):value;
  },set(target,key,value){if(key==='title'){root.querySelector('title').textContent=value;return true;}return Reflect.set(target,key,value,target);}});
  const scoped={
    document:documentProxy,
    get innerWidth(){return host.clientWidth;},get innerHeight(){return host.clientHeight;},get scrollY(){return 0;},
    get location(){return rendererURL;},
    history:{replaceState(_state,_title,url){if(url)rendererURL=new URL(url,rendererURL);},pushState(_state,_title,url){if(url)rendererURL=new URL(url,rendererURL);}},
    scrollTo(){},
    matchMedia(query){const media=real.matchMedia(query);return new Proxy(media,{get(target,key){
      if(key==='addEventListener')return (type,fn,opts)=>{if(disposed)return;target.addEventListener(type,fn,opts);mediaCleanup.push(()=>target.removeEventListener(type,fn,opts));};
      if(key==='addListener')return fn=>{if(disposed)return;target.addListener(fn);mediaCleanup.push(()=>target.removeListener(fn));};
      const value=Reflect.get(target,key,target);return typeof value==='function'?value.bind(target):value;
    }});},
    addEventListener(type,fn,opts){events.addEventListener(type,fn,options(opts));},
    removeEventListener(type,fn,opts){events.removeEventListener(type,fn,opts);},
    dispatchEvent(event){return events.dispatchEvent(event);},
    setTimeout(fn,delay,...args){if(disposed)return 0;const id=real.setTimeout(()=>{timers.delete(id);if(!disposed)fn(...args);},delay);timers.add(id);return id;},
    clearTimeout(id){timers.delete(id);real.clearTimeout(id);},
    setInterval(fn,delay,...args){if(disposed)return 0;const id=real.setInterval(()=>{if(!disposed)fn(...args);},delay);intervals.add(id);return id;},
    clearInterval(id){intervals.delete(id);real.clearInterval(id);},
    requestAnimationFrame(fn){if(disposed)return 0;const id=real.requestAnimationFrame(time=>{frames.delete(id);if(!disposed)fn(time);});frames.add(id);return id;},
    cancelAnimationFrame(id){frames.delete(id);real.cancelAnimationFrame(id);},
    MutationObserver:class extends real.MutationObserver{constructor(fn){super(fn);observers.add(this);}},
    ResizeObserver:class extends real.ResizeObserver{constructor(fn){super(fn);observers.add(this);}},
    otohaAnnotationActive:()=>!!page.querySelector('style[data-browser-comment-cursor-style]')
  };
  const win=new Proxy(scoped,{get(target,key){if(key in target)return target[key];const value=real[key];return typeof value==='function'&&!/^[A-Z]/.test(String(key))?value.bind(real):value;}});
  for(const type of ['blur','focus','visibilitychange','beforeprint','afterprint','load'])real.addEventListener(type,()=>events.dispatchEvent(new real.Event(type)),{signal:abort.signal});
  const resize=new real.ResizeObserver(()=>events.dispatchEvent(new real.Event('resize')));resize.observe(host);observers.add(resize);
  // Annotation owns the pointer and gestures while active. Do not fight its
  // native cursor replacement with an !important default cursor override.
  const annotationStyle=page.createElement('style');annotationStyle.textContent='';head.append(annotationStyle);
  const syncAnnotation=()=>{annotationStyle.textContent=scoped.otohaAnnotationActive()?'html,body,body *{cursor:none!important;user-select:none!important}':'';};
  const annotationObserver=new real.MutationObserver(syncAnnotation);annotationObserver.observe(page.head,{childList:true,subtree:true});observers.add(annotationObserver);syncAnnotation();
  function dispose(){if(disposed)return;events.dispatchEvent(new real.Event('pagehide'));disposed=true;abort.abort();for(const id of timers)real.clearTimeout(id);for(const id of intervals)real.clearInterval(id);for(const id of frames)real.cancelAnimationFrame(id);for(const observer of observers)observer.disconnect();for(const cleanup of mediaCleanup)cleanup();shadow.replaceChildren();}
  try{
    const names=['window','document','location','history','getComputedStyle','requestAnimationFrame','cancelAnimationFrame','setTimeout','clearTimeout','setInterval','clearInterval','MutationObserver','ResizeObserver'];
    const values=[win,documentProxy,rendererURL,scoped.history,real.getComputedStyle.bind(real),...names.slice(5).map(key=>scoped[key])];
    // The standalone renderer expects a viewport whose origin is (0,0).
    // Adapt only its private rect reads; DOM methods and annotation hit tests
    // keep native page coordinates. Pointer deltas and SVG CTMs stay native.
    const runtimeCode=scripts.join('\n').replace(/\b([A-Za-z_$][\w$]*(?:\[[^\]\n]+\])?)\.getBoundingClientRect\(\)/g,'otohaRect($1)');
    const otohaRect=element=>{const r=element.getBoundingClientRect(),origin=host.getBoundingClientRect();return new real.DOMRect(r.x-origin.x,r.y-origin.y,r.width,r.height);};
    names.push('otohaRect');values.push(otohaRect);
    const runtime=new Function(...names,runtimeCode+'\nreturn {Archify,archifyI18nData};');
    Object.assign(scoped,runtime(...values));
  }catch(error){dispose();throw error;}
  return {window:win,document:documentProxy,shadow,dispose};
}
