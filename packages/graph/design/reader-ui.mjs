// Reader chrome is localized independently of the authored graph. Only UI
// literals or explicitly registered chrome nodes are passed to this function.
export function otohaUIText(value, locale, messages) {
  if(locale!=='en')return value;
  const source=String(value);
  if(Object.hasOwn(messages,source))return messages[source];
  // HTML fragments in the legacy viewer contain literal UI copy around markup.
  // Replace catalog phrases once; never run this on assembled model content.
  const cache=otohaUIText.patternCache||(otohaUIText.patternCache=new WeakMap());
  let pattern=cache.get(messages);
  if(!pattern){pattern=new RegExp(Object.keys(messages).sort((a,b)=>b.length-a.length).map(s=>s.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')).join('|'),'g');cache.set(messages,pattern);}
  return source.replace(pattern,match=>messages[match]);
}

export function installOtohaChrome(doc,t) {
  const text=[],attributes=[];
  const walk=doc.createTreeWalker(doc.body,4);
  while(walk.nextNode()){
    const node=walk.currentNode;
    if(!node.parentElement.closest('script,style,textarea,pre')&&/[\u3400-\u9fff]/.test(node.data))text.push([node,node.data]);
  }
  for(const el of doc.body.querySelectorAll('*'))for(const name of ['title','aria-label','placeholder']){
    const value=el.getAttribute(name);if(value&&/[\u3400-\u9fff]/.test(value))attributes.push([el,name,value]);
  }
  const icons={'settings-toggle':'settings','settings-close':'close','canvas-focus':'focus','detail-toggle':'panel','team-open':'team','back':'back','density-compact':'compact','density-detailed':'detailed','collapse-submaps':'collapse','graph-data':'code','view-notes':'notes','detail-mode':'float','detail-close':'close','expand':'enter','source-open':'external','source-copy':'copy','source-close':'close','graph-close':'close','view-notes-close':'close','team-close':'close','team-sync':'sync'};
  const iconNames=new Map();
  for(const [id,icon]of Object.entries(icons)){const el=doc.getElementById(id);if(!el)continue;iconNames.set(id,el.getAttribute('aria-label')||el.title||el.textContent.trim());el.classList.add('otoha-icon-button');el.dataset.icon=icon;}
  function translate(){
    for(const [node,source]of text)if(node.isConnected)node.data=t(source);
    for(const [el,name,source]of attributes)el.setAttribute(name,t(source));
    for(const id of Object.keys(icons)){
      const el=doc.getElementById(id);if(!el)continue;
      const label=t(iconNames.get(id));
      el.setAttribute('aria-label',label);el.title=label;
    }
  }
  return {translate};
}

export function installOtohaFrameUI(win,locale,catalogs) {
  const doc=win.document,source=win.archifyI18nData?.messages||{},target=catalogs[locale]||catalogs.en;
  // Capture only renderer chrome. Authored SVG labels, cards and story text
  // are excluded, even when their wording happens to match a catalog phrase.
  const translations=new Map(Object.entries(source).filter(([key,value])=>target[key]&&!value.includes('{')).map(([key,value])=>[value,target[key]]));
  const roots=doc.querySelectorAll('.toolbar,.diagram-nav,.node-finder,.diagram-guide,.overview-map,.route-probe,.semantic-lens');
  for(const root of roots){
    const walker=doc.createTreeWalker(root,4);while(walker.nextNode()){const node=walker.currentNode,key=node.data.trim();if(translations.has(key))node.data=node.data.replace(key,translations.get(key));}
    for(const el of [root,...root.querySelectorAll('*')])for(const name of ['title','aria-label','placeholder']){const value=el.getAttribute(name);if(translations.has(value))el.setAttribute(name,translations.get(value));}
  }
  if(win.archifyI18nData){win.archifyI18nData.messages=target;win.archifyI18nData.locale=locale;}win.Archify.locale=locale;
  const icons={'btn-present':'present','btn-export':'export','btn-route-probe':'route','btn-overview-map':'map','btn-semantic-lens':'layers','btn-node-finder':'search','btn-diagram-guide':'help','btn-motion':'motion'};
  for(const [id,icon]of Object.entries(icons)){const el=doc.getElementById(id);if(el){el.classList.add('otoha-icon-button');el.dataset.icon=icon;}}
}
