// Runs in the scoped canvas; the reader owns identity, navigation and state.
export function installOtohaNodes(win, options) {
  const doc=win.document,svg=doc.querySelector('.diagram-container > svg');
  if(!svg)return;
  const NS='http://www.w3.org/2000/svg', cache=new Map(), originals=new Map();
  const entities=new Map(options.model.entities.map(e=>[e.id,e]));
  const childView=id=>options.model.views.find(v=>v.scope===id);
  const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const expanded=new Set(options.expanded||[]);
  const en=options.locale==='en';
  const inspectLabel=label=>en?'Details: '+label:'查看 '+label+' 详情';
  const submapLabel=(label,open)=>en?(open?'Collapse ':'Expand ')+label+' submap; double-click to enter full view':(open?'收起 ':'展开 ')+label+' 子图；双击进入完整视图';
  let density=options.density||'detailed',selected=options.selected,plan,pending=null;
  const pathString=points=>points.map((p,i)=>(i?'L':'M')+p.join(' ')).join(' ');
  const make=(tag,attrs={},parent)=>{const el=doc.createElementNS(NS,tag);for(const [k,v] of Object.entries(attrs))el.setAttribute(k,v);if(parent)parent.append(el);return el;};
  function scene(view,compact=false) {
    const key=view.id+':'+compact;if(cache.has(key))return cache.get(key);
    const source=view.id===options.view.id?svg:new win.DOMParser().parseFromString(options.views[view.id],'text/html').querySelector('.diagram-container > svg');
    const nodes=view.placements.map(p=>({id:p.entity,x:p.pos[0],y:p.pos[1],w:(p.size||[170,76])[0],h:(p.size||[170,76])[1],label:p.label||entities.get(p.entity).label,sublabel:p.sublabel??entities.get(p.entity).sublabel??'',tag:p.tag||'',kind:p.type||entities.get(p.entity).type}));
    const edges=[...source.querySelectorAll('path[data-edge-id][data-composition-points]')].map(el=>({id:el.dataset.edgeId,from:el.dataset.edgeFrom,to:el.dataset.edgeTo,points:el.dataset.compositionPoints.split(';').map(p=>p.split(',').map(Number)),className:el.getAttribute('class'),marker:el.getAttribute('marker-end')}));
    let width=source.viewBox.baseVal.width,height=source.viewBox.baseVal.height;
    if(compact){
      const minX=Math.min(...nodes.map(n=>n.x))-16,minY=Math.min(...nodes.map(n=>n.y))-20;
      const maxX=Math.max(...nodes.map(n=>n.x+n.w))+16,maxY=Math.max(...nodes.map(n=>n.y+n.h))+20;
      const scale=Math.min(520/(maxX-minX),245/(maxY-minY));
      for(const n of nodes){n.x=(n.x-minX)*scale;n.y=(n.y-minY)*scale;n.w*=scale;n.h*=scale;}
      for(const e of edges)e.points=e.points.map(p=>[(p[0]-minX)*scale,(p[1]-minY)*scale]);
      width=(maxX-minX)*scale;height=(maxY-minY)*scale;
    }
    const value={nodes,edges,width,height};cache.set(key,value);return value;
  }
  function makePlan(view,prefix='',compact=false,ancestors=[]) {
    const data=scene(view,compact),sizes={},children={};
    for(const n of data.nodes){
      const key=prefix+n.id,child=childView(n.id);
      if(child&&expanded.has(key)&&!ancestors.includes(child.id)){
        children[n.id]=makePlan(child,key+'/',true,[...ancestors,view.id]);
        sizes[n.id]={w:Math.max(460,children[n.id].width+40),h:children[n.id].height+100};
      }else sizes[n.id]={w:Math.max(n.w,compact?116:220),h:Math.max(n.h,compact?88:114)};
    }
    const layout=otohaExpansionLayout(data.nodes,sizes,data.width,data.height,compact?.4:.45);
    return {...layout,data,children,prefix,compact,view};
  }
  function textLines(parent,label,x,y,width,size,maxLines=2,anchor='start') {
    const limit=width/size, chunk=Math.max(1,Math.floor(limit/.55));
    const chars=(label.match(/[A-Za-z0-9]+|[^A-Za-z0-9]/g)||[]).flatMap(token=>token.length>chunk?token.match(new RegExp('.{1,'+chunk+'}','g')):token),lines=[];let line='',units=0;
    for(const c of chars){const unit=/^[\x00-\x7F]+$/.test(c)?c.length*.55:1;if(units+unit>limit&&line){lines.push(line.trim());line='';units=0;}line+=c;units+=unit;}
    if(line)lines.push(line);
    const shown=lines.slice(0,maxLines);if(lines.length>maxLines)shown[maxLines-1]=shown[maxLines-1].slice(0,-1)+'…';
    const t=make('text',{x,y,'font-size':size,'font-weight':600,'text-anchor':anchor,class:'otoha-label'},parent);
    shown.forEach((line,i)=>{make('tspan',{x,dy:i?size*1.35:0},t).textContent=line;});return t;
  }
  const icon={info:'<circle cx="9" cy="9" r="6.5"/><path d="M9 8v5M9 5v.5"/>',sub:'<rect x="2" y="2" width="11" height="8" rx="2"/><path d="M5 13h11V6M7 6h1m2 0h1"/>',enter:'<path d="M4 14 14 4M5 4h9v9"/>',collapse:'<path d="M4 11 9 6l5 5"/>'};
  function button(parent,{x,y,w=30,action,id,key,label,caption='',glyph='info',pressed}){
    const g=make('g',{class:'otoha-action otoha-'+action,transform:`translate(${x} ${y})`,role:'button',tabindex:0,'aria-label':label,'data-otoha-action':action,'data-otoha-entity':id,'data-otoha-key':key},parent);
    if(pressed!==undefined)g.setAttribute('aria-expanded',String(pressed));
    make('title',{},g).textContent=label;
    make('rect',{width:w,height:28,rx:7,class:'otoha-action-surface'},g);
    const symbol=make('g',{transform:'translate(6 5)',class:'otoha-icon','aria-hidden':true},g);symbol.innerHTML=icon[glyph];
    if(caption)make('text',{x:28,y:18,class:'otoha-action-label','font-size':10},g).textContent=caption;
    return g;
  }
  function drawNode(group,n,b,current){
    const key=current.prefix+n.id,child=childView(n.id),nested=current.children[n.id],mini=current.compact;
    group.replaceChildren();group.classList.add('otoha-card');group.dataset.otohaNode=n.id;group.dataset.otohaKey=key;
    group.setAttribute('role','group');group.removeAttribute('tabindex');group.removeAttribute('aria-pressed');group.setAttribute('aria-label',n.label);
    group.dataset.otohaExpanded=String(!!nested);
    make('title',{},group).textContent=[n.label,n.sublabel,n.tag].filter(Boolean).join(' · ');
    make('rect',{...{x:b.x,y:b.y,width:b.w,height:b.h},rx:10,class:'c-mask'},group);
    make('rect',{x:b.x,y:b.y,width:b.w,height:b.h,rx:10,class:'c-'+n.kind+' otoha-card-surface','stroke-width':1.3,role:'button',tabindex:0,'aria-label':(en?'Select ':'选择 ')+n.label,'data-otoha-action':'select','data-otoha-entity':n.id,'data-otoha-key':key},group);
    make('rect',{x:b.x+1,y:b.y+1,width:b.w-2,height:Math.min(42,b.h-2),rx:9,fill:'url(#otoha-card-gloss)','pointer-events':'none'},group);
    const content=make('g',{'pointer-events':'none'},group);
    if(nested){
      textLines(content,n.label,b.x+16,b.y+27,b.w-220,17);
      make('text',{x:b.x+16,y:b.y+48,class:'otoha-caption','font-size':10},content).textContent=en?'Next level · '+nested.data.nodes.length+' modules':'子一级 · '+nested.data.nodes.length+' 个模块';
      button(group,{x:b.x+b.w-200,y:b.y+13,w:91,action:'submap',id:n.id,key,label:submapLabel(n.label,true),caption:en?'Collapse':'收起子图',glyph:'collapse',pressed:true});
      button(group,{x:b.x+b.w-103,y:b.y+13,w:58,action:'enter',id:n.id,key,label:en?'Enter full submap: '+n.label:'进入 '+n.label+' 完整子图',caption:en?'Open':'进入',glyph:'enter'});
      button(group,{x:b.x+b.w-39,y:b.y+13,action:'inspect',id:n.id,key,label:inspectLabel(n.label)});
      make('path',{d:`M${b.x+14} ${b.y+62}H${b.x+b.w-14}`,class:'otoha-divider'},group);
      const inner=make('g',{transform:`translate(${b.x+20} ${b.y+78})`,class:'otoha-submap','data-otoha-submap':key},group);
      drawMini(inner,nested);
    }else{
      const labelY=b.y+(mini?23:29),font=mini?15:16;
      textLines(content,n.label,b.x+12,labelY,b.w-(mini?24:52),font,2);
      if(!mini){
        button(group,{x:b.x+b.w-37,y:b.y+8,action:'inspect',id:n.id,key,label:inspectLabel(n.label)});
        const description=make('g',{class:'otoha-detail-copy'},content);
        make('text',{x:b.x+12,y:b.y+65,'font-size':10,class:'otoha-caption'},description).textContent=n.sublabel.length>25?n.sublabel.slice(0,24)+'…':n.sublabel;
        if(n.tag)make('text',{x:b.x+12,y:b.y+81,'font-size':8.5,class:'otoha-caption'},description).textContent=n.tag.length>22?n.tag.slice(0,21)+'…':n.tag;
        const claim=entities.get(n.id)?.maturity?.implementation;
        make('text',{x:b.x+12,y:b.y+b.h-16,'font-size':9,class:'otoha-caption'},description).textContent=options.labels[claim?.effective||claim?.value]||'待复核';
      }else button(group,{x:b.x+b.w-36,y:b.y+b.h-33,action:'inspect',id:n.id,key,label:inspectLabel(n.label)});
      if(child){
        const w=mini?70:83;
        button(group,{x:mini?b.x+5:b.x+b.w-w-9,y:b.y+b.h-35,w,action:'submap',id:n.id,key,label:submapLabel(n.label,false),caption:(en?'Map ':'子图 ')+child.placements.length,glyph:'sub',pressed:false});
      }
    }
  }
  function drawMini(group,current){
    for(const edge of current.data.edges){const from=current.data.nodes.find(n=>n.id===edge.from),to=current.data.nodes.find(n=>n.id===edge.to);if(!from||!to)continue;const points=otohaExpandedRoute(edge.points,from,to,current);make('path',{d:pathString(points),class:edge.className+' otoha-mini-edge','marker-end':edge.marker,'data-otoha-edge':edge.id},group);}
    for(const n of current.data.nodes){const g=make('g',{},group);drawNode(g,n,current.boxes[n.id],current);}
  }
  for(const g of svg.querySelectorAll(':scope > g[data-node-id]'))originals.set(g.dataset.nodeId,g);
  const edgeEls=[...svg.querySelectorAll(':scope > path[data-edge-id][data-composition-points]')];
  const labelEls=[...svg.querySelectorAll(':scope > g[data-edge-id]')].map(el=>({el,x:Number(el.querySelector('text')?.getAttribute('x')||0),y:Number(el.querySelector('text')?.getAttribute('y')||0)}));
  const legend=svg.querySelector('[data-legend]'),legendY=legend?.getBBox().y||0;
  function markSelected(){
    for(const g of svg.querySelectorAll('[data-otoha-node]')){const active=g.dataset.otohaNode===selected;g.classList.toggle('otoha-selected',active);g.querySelector(':scope > [data-otoha-action="select"]')?.setAttribute('aria-pressed',String(active));}
  }
  function redraw({restore=false}={}){
    const oldPlan=plan, camera=win.Archify?.view, previous=camera?.logicalViewport();
    plan=makePlan(options.view);
    for(const n of plan.data.nodes){const g=originals.get(n.id);if(g)drawNode(g,n,plan.boxes[n.id],plan);}
    const routes=new Map();
    for(const el of edgeEls){const edge=plan.data.edges.find(e=>e.id===el.dataset.edgeId);if(!edge)continue;const from=plan.data.nodes.find(n=>n.id===edge.from),to=plan.data.nodes.find(n=>n.id===edge.to);const points=otohaExpandedRoute(edge.points,from,to,plan);routes.set(edge.id,points);el.setAttribute('d',pathString(points));el.dataset.compositionPoints=points.map(p=>p.join(',')).join(';');}
    svg.querySelectorAll('[data-otoha-label-leader]').forEach(e=>e.remove());
    const occupied=Object.values(plan.boxes).map(b=>({x:b.x-7,y:b.y-7,width:b.w+14,height:b.h+14}));
    const intersects=(a,b)=>a.x<b.x+b.width&&a.x+a.width>b.x&&a.y<b.y+b.height&&a.y+a.height>b.y;
    for(const {el,x,y} of labelEls){
      const target=plan.point([x,y]),points=routes.get(el.dataset.edgeId)||[],bbox=el.getBBox(),candidates=[];
      for(let i=1;i<points.length;i++){
        const a=points[i-1],b=points[i],horizontal=a[1]===b[1],length=Math.hypot(b[0]-a[0],b[1]-a[1]);
        if(length<24)continue;
        for(const fraction of [.5,.25,.75])for(const side of [-1,1])for(const offset of [17,36,64,96,140]){
          const center=[a[0]+(b[0]-a[0])*fraction,a[1]+(b[1]-a[1])*fraction];
          const next=horizontal?[center[0],center[1]+side*offset]:[center[0]+side*(bbox.width/2+offset),center[1]];
          const rect={x:bbox.x+next[0]-x,y:bbox.y+next[1]-y,width:bbox.width,height:bbox.height};
          const collisions=occupied.filter(b=>intersects(rect,b)).length+(rect.x<6||rect.y<6||rect.x+rect.width>plan.width-6||rect.y+rect.height>plan.height-6?1:0);
          candidates.push({next,rect,center,offset,score:collisions*100000+Math.hypot(next[0]-target[0],next[1]-target[1])+(horizontal?0:30)+(length<bbox.width+16&&horizontal?200:0)});
        }
      }
      candidates.sort((a,b)=>a.score-b.score);
      const choice=candidates[0],next=choice?.next||target;
      if(choice){occupied.push(choice.rect);if(choice.offset>30){const r=choice.rect,end=[Math.max(r.x,Math.min(r.x+r.width,choice.center[0])),Math.max(r.y,Math.min(r.y+r.height,choice.center[1]))];const leader=make('path',{d:pathString([choice.center,end]),'data-otoha-label-leader':true,fill:'none',stroke:'var(--arrow)',opacity:.45,'stroke-width':.7,'stroke-dasharray':'2 3','pointer-events':'none'},svg);svg.insertBefore(leader,el);}}
      el.setAttribute('transform',`translate(${next[0]-x} ${next[1]-y})`);
    }
    if(legend)legend.setAttribute('transform',`translate(0 ${plan.point([0,legendY])[1]-legendY})`);
    svg.setAttribute('viewBox',`0 0 ${plan.width} ${plan.height}`);
    doc.documentElement.dataset.otohaDensity=density;markSelected();
    options.onLayout?.();
    // Rebuild radar bounds from the same rendered geometry.
    win.Archify?.focus?.refreshGeometry?.();win.Archify?.radar?.refresh?.();
    if(oldPlan&&previous&&!restore){const origin=oldPlan.inverse([previous.x+previous.width/2,previous.y+previous.height/2]),center=plan.point(origin);win.requestAnimationFrame(()=>camera.centerAt(center[0],center[1],{scale:previous.scale,instant:true}));}
    options.onState?.([...expanded]);
  }
  function cancelPending(){if(pending){win.clearTimeout(pending.timer);pending=null;}}
  function toggle(key){cancelPending();expanded.has(key)?expanded.delete(key):expanded.add(key);redraw();svg.querySelector('[data-otoha-action="submap"][data-otoha-key="'+CSS.escape(key)+'"]')?.focus({preventScroll:true});}
  function handle(event){
    if(win.otohaAnnotationActive?.()||doc.querySelector('style[data-browser-comment-cursor-style]'))return;
    const action=event.target.closest('[data-otoha-action]'),node=event.target.closest('[data-otoha-node]');
    if(!action&&!node)return;
    if(event.type==='keydown'&&!['Enter',' ','Escape'].includes(event.key))return;
    if(event.type==='keydown'&&event.key==='Escape')return;
    if(event.type==='click'&&doc.querySelector('.diagram-container')?.hasAttribute('data-just-panned'))return;
    event.preventDefault();event.stopImmediatePropagation();
    const id=action?.dataset.otohaEntity||node.dataset.otohaNode,key=action?.dataset.otohaKey||node.dataset.otohaKey,kind=action?.dataset.otohaAction||'select';
    if(event.type==='dblclick'){if(kind==='submap'){cancelPending();options.onEnter(id);}return;}
    if(kind==='select'){selected=id;markSelected();options.onSelect(id);return;}
    if(kind==='inspect'){cancelPending();selected=id;markSelected();options.onInspect(id,action.getBoundingClientRect());return;}
    if(kind==='enter'||(kind==='submap'&&event.shiftKey)){cancelPending();options.onEnter(id);return;}
    if(kind==='submap'){
      selected=id;markSelected();options.onSelect(id);
      if(event.type==='keydown'||event.detail===0){toggle(key);return;}
      if(event.detail>1){cancelPending();return;}
      cancelPending();pending={key,timer:win.setTimeout(()=>toggle(key),500)};
    }
  }
  const style=doc.createElement('style');style.textContent=`
    html,body,.diagram-container,svg{cursor:default}.otoha-card{cursor:default}.otoha-card-surface{cursor:pointer;transition:stroke .14s,filter .14s}
    .otoha-card-surface:hover{stroke:var(--arrow-emphasis);stroke-width:2}
    .otoha-selected>.otoha-card-surface{stroke:var(--arrow-emphasis);stroke-width:2.5;filter:drop-shadow(0 0 7px color-mix(in srgb,var(--arrow-emphasis) 35%,transparent))}
    .otoha-label{fill:var(--text)}.otoha-caption{fill:var(--text-muted)}.otoha-divider{stroke:var(--panel-border);stroke-width:1}
    .otoha-action{cursor:pointer;outline:none}.otoha-action-surface{fill:var(--toolbar-bg);stroke:var(--toolbar-border);stroke-width:.7}
    .otoha-action:hover>.otoha-action-surface,.otoha-action:focus-visible>.otoha-action-surface{fill:var(--toolbar-hover);stroke:var(--arrow-emphasis);stroke-width:1.5}
    .otoha-action:active>.otoha-action-surface{fill:var(--panel)}.otoha-icon{fill:none;stroke:var(--arrow-emphasis);stroke-width:1.5;stroke-linecap:round;stroke-linejoin:round;pointer-events:none}
    .otoha-action-label{fill:var(--text);pointer-events:none}.otoha-card-surface:focus-visible{outline:none;stroke:var(--arrow-emphasis);stroke-width:3}
    [data-otoha-density=compact] .otoha-detail-copy{display:none}.otoha-mini-edge{fill:none;stroke-width:1.2}
    .otoha-submap{animation:otoha-reveal .16s ease-out}.otoha-card text{opacity:1!important}
    svg[data-focus-active] [data-node-id],svg[data-focus-active] [data-edge-id]{opacity:1!important}
    @keyframes otoha-reveal{from{opacity:0}to{opacity:1}}
    @media(prefers-reduced-motion:reduce){.otoha-submap{animation:none}.otoha-card-surface{transition:none}}
  `;doc.head.append(style);
  const defs=svg.querySelector('defs');const gloss=make('linearGradient',{id:'otoha-card-gloss',x1:0,y1:0,x2:0,y2:1},defs);make('stop',{offset:0,'stop-color':'#fff','stop-opacity':.075},gloss);make('stop',{offset:1,'stop-color':'#fff','stop-opacity':0},gloss);
  // Cache the pristine scene before touching its nodes or paths.
  win.Archify?.focus?.clear({updateUrl:false});win.Archify?.intentTrace?.clear();
  scene(options.view);redraw({restore:true});
  for(const type of ['click','dblclick','keydown'])doc.addEventListener(type,handle,true);
  win.addEventListener('pagehide',()=>{cancelPending();for(const type of ['click','dblclick','keydown'])doc.removeEventListener(type,handle,true);},{once:true});
  return {cancel:cancelPending,select(id){selected=id;markSelected();},density(value){density=value;doc.documentElement.dataset.otohaDensity=value;},collapseAll(){cancelPending();expanded.clear();redraw();},state(){return {expanded:[...expanded],density,selected};}};
}
