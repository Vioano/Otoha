import { otohaFilterPresets, filterGraphEntities } from './filters.mjs';
import { digest, problem } from './model.mjs';
import { projectTasks } from './tasks.mjs';

export const strategies = ['overview', 'local', 'reach', 'path', 'cycles', 'view', 'board', 'full'];
const sorted = xs => [...xs].sort((a,b) => String(a.id).localeCompare(String(b.id), 'en'));
export function topology(model) {
  return { entities: sorted(model.entities).map(e => ({id:e.id, parent:e.parent || null})), ...(model.tasks?.length ? { tasks: sorted(model.tasks).map(t => ({id:t.id, entities:[...t.entities].sort()})) } : {}), relations: sorted(model.relations).map(({id,from,to,kind}) => ({id,from,to,kind})) };
}
export const topologyHash = model => digest(JSON.stringify(topology(model)));
export function viewGraph(model, id) {
  const v = model.views.find(v => v.id === id);
  if (!v) problem('query/view', 'Unknown view', {id}, 404);
  const nodes = new Map(model.entities.map(e => [e.id,e])), edges = new Map(model.relations.map(e => [e.id,e]));
  return { entities:v.placements.map(p => nodes.get(p.entity)), relations:v.relations.map(r => edges.get(r.relation)) };
}
export function assertProjectionCoverage(model) {
  const entities = new Set(), relations = new Set();
  for (const v of model.views) {
    const g = viewGraph(model,v.id);
    g.entities.forEach(e => entities.add(e.id)); g.relations.forEach(e => relations.add(e.id));
  }
  const missing = { entities:model.entities.filter(e => !entities.has(e.id)).map(e => e.id), relations:model.relations.filter(e => !relations.has(e.id)).map(e => e.id) };
  if (missing.entities.length || missing.relations.length) problem('graph/unprojected', 'Every canonical node and relation must be available in a Human view', missing);
}
export function manifest(snapshot) {
  const m=snapshot.model;
  return {schema_version:1, cursor:snapshot.cursor, revision:snapshot.revision, evidenceRevision:snapshot.evidenceRevision, topologyHash:topologyHash(m), title:m.meta.title,
    counts:Object.fromEntries(['entities','relations','views','evidence','tasks'].map(k=>[k,(m[k]||[]).length])),
    views:m.views.map(v=>({id:v.id,title:v.title,scope:v.scope||null,entities:v.placements.length,relations:v.relations.length})),
    capabilities:{strategies, diff:true, resumableEvents:true, pinnedVersions:true, runtimeTemporalQueries:false, taskBoard:true, filters:{board:otohaFilterPresets('board'),view:otohaFilterPresets('canvas')}},
    defaults:{mode:'overview',detail:'summary',limit:100,maxBytes:65536},
    semantics:{direction:'Stored from → to; dependency impact depends on the authored edge convention.',time:'Cursor is an accepted model/evidence revision, not a runtime event clock.',crossLevel:'Containment never implies dataflow; missing boundary links remain unknown.'}};
}
function int(value, fallback, max, name) {
  const n=value===undefined?fallback:Number(value);
  if (!Number.isInteger(n)||n<0||n>max) problem('query/argument', `Invalid ${name}`, {max});
  return n;
}
export function normalizeQuery(q={}) {
  const allowed=['mode','target','from','to','view','expanded','depth','hops','direction','kinds','detail','limit','maxBytes','page','cursor','assignee','status','search','filter'];
  for(const k of Object.keys(q)) if(!allowed.includes(k)) problem('query/argument','Unknown query option',{key:k});
  const mode=q.mode||'overview', detail=q.detail||'summary', direction=q.direction||'out';
  if(!strategies.includes(mode)||!['summary','full'].includes(detail)||!['in','out','both'].includes(direction)) problem('query/argument','Invalid mode, detail or direction');
  const kinds=q.kinds===undefined?['call','dataflow','dependency','feedback']:(Array.isArray(q.kinds)?q.kinds:String(q.kinds).split(','));
  if(!kinds.length||kinds.some(k=>!['call','dataflow','dependency','feedback'].includes(k))) problem('query/argument','Invalid relation kinds');
  const query={mode,detail,direction,kinds:[...new Set(kinds)].sort(),depth:int(q.depth,0,1000,'depth'),hops:int(q.hops,1,1000,'hops'),limit:Math.max(1,int(q.limit,100,10000,'limit')),maxBytes:Math.max(1024,int(q.maxBytes,65536,16*1024*1024,'maxBytes'))};
  for(const k of ['target','from','to','view','assignee','status','search','filter']) if(q[k]!==undefined){if(typeof q[k]!=='string')problem('query/argument',`${k} must be a string`);query[k]=q[k];}
  if(q.expanded!==undefined){const keys=Array.isArray(q.expanded)?q.expanded:String(q.expanded).split(',').filter(Boolean);if(mode!=='view'||keys.some(k=>typeof k!=='string'||k.length>8000))problem('query/argument','expanded requires view mode and valid instance paths');query.expanded=[...new Set(keys)].sort();}
  if(query.status && !['todo','doing','review','done'].includes(query.status)) problem('query/argument','Invalid task status');
  if(mode!=='board' && ['assignee','status','search'].some(k=>q[k]!==undefined)) problem('query/argument','Task filters require board mode');
  if(query.filter!==undefined && (!['board','view'].includes(mode)||!otohaFilterPresets(mode==='board'?'board':'canvas').some(([id])=>id===query.filter)))problem('query/argument','Invalid filter for query mode');
  return query;
}
// Pages are pinned to both a snapshot and a normalized query, never live offsets.
export function paginate(records, identity, options={}) {
  const limit=Math.max(1,int(options.limit,100,10000,'limit')), maxBytes=Math.max(1024,int(options.maxBytes,65536,16*1024*1024,'maxBytes'));
  const fingerprint=digest(JSON.stringify(identity)); let start=0;
  if(options.page){
    let p; try{p=JSON.parse(Buffer.from(options.page,'base64url').toString());}catch{problem('query/page','Invalid page token');}
    if(p.fingerprint!==fingerprint||!Number.isInteger(p.offset)||p.offset<0||p.offset>=records.length) problem('query/page','Page belongs to a different query or revision; repeat the pinned query',{},409);
    start=p.offset;
  }
  let end=start,bytes=0;
  while(end<records.length&&end-start<limit){const size=Buffer.byteLength(JSON.stringify(records[end]));if(bytes+size>maxBytes)break;bytes+=size;end++;}
  if(end===start&&start<records.length) problem('query/item-too-large','One record exceeds maxBytes; use summary detail or increase maxBytes',{requiredBytes:Buffer.byteLength(JSON.stringify(records[start]))});
  return {records:records.slice(start,end),complete:start===0&&end===records.length,page:{offset:start,returned:end-start,total:records.length,hasMore:end<records.length,next:end<records.length?Buffer.from(JSON.stringify({fingerprint,offset:end})).toString('base64url'):null,recordBytes:bytes}};
}
export function queryGraph(snapshot, input={}) {
  const q=normalizeQuery(input), m=snapshot.model, byId=new Map(m.entities.map(e=>[e.id,e]));
  if(q.mode==='board'){
    const projection=projectTasks(m,q), records=projection.tasks.map(task=>({type:'task',value:q.detail==='full'?task:{id:task.id,title:task.title,status:task.status,assignees:task.assignees,entities:task.entities,blocked:task.blocked}}));
    return {schema_version:1,cursor:snapshot.cursor,revision:snapshot.revision,evidenceRevision:snapshot.evidenceRevision,query:q,selectionComplete:true,columns:projection.columns.map(c=>({status:c.status,count:c.taskIds.length})),notes:['Task entities are explicit module associations, not dataflow. Completion does not promote module maturity.'],...paginate(records,{cursor:snapshot.cursor,revision:snapshot.revision,query:q},{...q,page:input.page})};
  }
  const edges=sorted(m.relations.filter(e=>q.kinds.includes(e.kind))), children=new Map();
  for(const e of m.entities){const p=e.parent||null;if(!children.has(p))children.set(p,[]);children.get(p).push(e.id);}
  const requireNode=id=>{if(!byId.has(id))problem('query/entity','Unknown or missing entity',{id},404);};
  const adjacency=new Map(m.entities.map(e=>[e.id,[]]));
  for(const e of edges){if(q.direction!=='in')adjacency.get(e.from).push({id:e.to,edge:e});if(q.direction!=='out')adjacency.get(e.to).push({id:e.from,edge:e});}
  const neighbors=id=>adjacency.get(id)||[];
  const traverse=(seeds,max)=>{const seen=new Set(seeds),queue=[...seeds].map(id=>[id,0]);for(let i=0;i<queue.length;i++){const [id,d]=queue[i];if(d>=max)continue;for(const n of neighbors(id))if(!seen.has(n.id)){seen.add(n.id);queue.push([n.id,d+1]);}}return seen;};
  let ids=new Set(), selectedEdges, groups=[], notes=[], path=null, customNodes;
  if(q.mode==='view'){
    const included=new Map(),expanded=new Set(q.expanded||[]),applied=new Set();
    function collect(viewId,prefix='',ancestors=[]){
      const g=viewGraph(m,viewId);g.entities.forEach(e=>ids.add(e.id));g.relations.filter(e=>q.kinds.includes(e.kind)).forEach(e=>included.set(e.id,e));
      for(const e of g.entities){const key=prefix+e.id,child=m.views.find(v=>v.scope===e.id);if(child&&expanded.has(key)&&!ancestors.includes(child.id)&&child.id!==viewId){applied.add(key);collect(child.id,key+'/',[...ancestors,viewId]);}}
    }
    collect(q.view);selectedEdges=[...included.values()];
    const inactive=[...expanded].filter(k=>!applied.has(k));if(inactive.length)notes.push('Inactive expansion paths (parent folded, missing, or recursive): '+inactive.join(', '));
  } else if(q.mode==='full') ids=new Set(byId.keys());
  else if(q.mode==='overview'){
    const representative=new Map(),members=new Map();
    for(const e of m.entities){const ancestry=[e.id];let current=e;while(current.parent){ancestry.unshift(current.parent);current=byId.get(current.parent);}const rep=ancestry[Math.min(q.depth,ancestry.length-1)];representative.set(e.id,rep);if(!members.has(rep))members.set(rep,[]);members.get(rep).push(e.id);}
    ids=new Set(members.keys());customNodes=[...members].map(([id,memberIds])=>({...byId.get(id),aggregation:{memberIds:memberIds.sort(),count:memberIds.length}}));
    const aggregated=new Map();
    for(const e of edges){const from=representative.get(e.from),to=representative.get(e.to);if(from===to){continue;}const key=JSON.stringify([from,to,e.kind]);if(!aggregated.has(key))aggregated.set(key,{id:'aggregate-'+digest(key).slice(0,24),from,to,kind:e.kind,label:e.kind,sourceRelationIds:[]});aggregated.get(key).sourceRelationIds.push(e.id);}
    selectedEdges=[...aggregated.values()];notes.push('Aggregated relationships retain source IDs. Internal edges are hidden by folding, not absent. Mixed-depth containment is metadata, not a flow edge.');
  } else if(q.mode==='local'){
    requireNode(q.target);ids.add(q.target);let level=[q.target];for(let i=0;i<q.depth;i++){level=level.flatMap(id=>children.get(id)||[]);level.forEach(id=>ids.add(id));}ids=traverse(ids,q.hops);
  } else if(q.mode==='reach'){
    requireNode(q.target);ids=traverse([q.target],Infinity);notes.push('Structural reachability along the selected edge types; it does not prove runtime impact or cross-level connectivity.');
  } else if(q.mode==='path'){
    requireNode(q.from);requireNode(q.to);const queue=[q.from],previous=new Map([[q.from,null]]);
    for(let i=0;i<queue.length&&!previous.has(q.to);i++)for(const n of neighbors(queue[i]))if(!previous.has(n.id)){previous.set(n.id,{node:queue[i],edge:n.edge});queue.push(n.id);}
    selectedEdges=[];path=[];
    if(previous.has(q.to)){let id=q.to;while(id!==q.from){path.unshift(id);const p=previous.get(id);selectedEdges.unshift(p.edge);id=p.node;}path.unshift(q.from);ids=new Set(path);}
    notes.push('One deterministic shortest-hop witness, not all possible paths. No runtime ordering is inferred.');
  } else if(q.mode==='cycles'){
    // Iterative Kosaraju avoids recursion limits. Singleton SCCs count only with a self-loop.
    const adjacency=new Map(m.entities.map(e=>[e.id,[]])),reverse=new Map(m.entities.map(e=>[e.id,[]]));
    for(const e of edges){adjacency.get(e.from).push(e.to);reverse.get(e.to).push(e.from);}
    const visited=new Set(),order=[];
    for(const root of [...byId.keys()].sort()){if(visited.has(root))continue;const stack=[[root,false]];while(stack.length){const [id,done]=stack.pop();if(done){order.push(id);continue;}if(visited.has(id))continue;visited.add(id);stack.push([id,true]);for(const n of adjacency.get(id))if(!visited.has(n))stack.push([n,false]);}}
    const assigned=new Set();
    for(const root of order.reverse()){if(assigned.has(root))continue;const members=[],stack=[root];assigned.add(root);while(stack.length){const id=stack.pop();members.push(id);for(const n of reverse.get(id))if(!assigned.has(n)){assigned.add(n);stack.push(n);}}members.sort();if((members.length>1||edges.some(e=>e.from===root&&e.to===root))&&(!q.target||members.includes(q.target))){groups.push({id:'scc-'+digest(JSON.stringify(members)).slice(0,24),members});members.forEach(id=>ids.add(id));}}
    if(q.target)requireNode(q.target);
    selectedEdges=edges.filter(e=>groups.some(g=>g.members.includes(e.from)&&g.members.includes(e.to)));notes.push('Cyclic strongly connected regions; not an enumeration of cycles or proof of a business feedback loop.');
  }
  let filterSummary;
  if(q.mode==='view'&&q.filter){
    let f;try{f=filterGraphEntities({...m,relations:edges},[...ids],q.filter,q.target);}catch(e){problem('query/argument',e.message);}
    ids=new Set(f.ids);selectedEdges=selectedEdges.filter(e=>ids.has(e.from)&&ids.has(e.to));
    filterSummary={strategy:q.filter,target:q.target||null,matched:f.matchedIds.length,context:f.contextIds.length,hidden:f.hiddenCount,matchingOutsideView:f.matchingOutsideView};
    notes.push('Filtered view: hidden records still exist. Containment ancestors may remain as context. Edges follow stored arrows, not runtime causality.');
  }
  selectedEdges??=edges.filter(e=>ids.has(e.from)&&ids.has(e.to));
  const boundary=q.mode==='overview'?[]:edges.filter(e=>ids.has(e.from)!==ids.has(e.to)).map(e=>({...e,externalEntity:ids.has(e.from)?e.to:e.from}));
  const node=e=>q.detail==='full'?e:{id:e.id,label:e.label,type:e.type,...(e.parent?{parent:e.parent}:{}),children:(children.get(e.id)||[]).length,submap:m.views.find(v=>v.scope===e.id)?.id||null,...(e.aggregation?{aggregation:e.aggregation}:{})};
  const entities=sorted(customNodes||m.entities.filter(e=>ids.has(e.id))).map(node);
  const evidenceIds=new Set(q.detail==='full'?entities.flatMap(e=>e.evidence||[]):[]);
  const records=[...entities.map(value=>({type:'entity',value})),...sorted(selectedEdges).map(value=>({type:'relation',value})),...sorted(boundary).map(value=>({type:'boundary',value})),...sorted(groups).map(value=>({type:'group',value})),...sorted(m.evidence.filter(e=>evidenceIds.has(e.id))).map(value=>({type:'evidence',value}))];
  if(q.mode==='full') records.push(...projectTasks(m).tasks.map(value=>({type:'task',value})));
  const result=paginate(records,{cursor:snapshot.cursor,revision:snapshot.revision,evidenceRevision:snapshot.evidenceRevision,query:q}, {...q,page:input.page});
  return {schema_version:1,cursor:snapshot.cursor,revision:snapshot.revision,evidenceRevision:snapshot.evidenceRevision,query:q,selectionComplete:true,notes,...(filterSummary?{filter:filterSummary}:{}),...(path?{path}:{}),...result};
}
export function diffSnapshots(before,after,options={}) {
  const changes=[];
  for(const collection of ['entities','relations','views','evidence','tasks']){
    const a=new Map((before.model[collection]||[]).map(x=>[x.id,x])),b=new Map((after.model[collection]||[]).map(x=>[x.id,x]));
    for(const id of [...new Set([...a.keys(),...b.keys()])].sort()){
      if(!a.has(id))changes.push({collection,id,operation:'added',after:b.get(id)});
      else if(!b.has(id))changes.push({collection,id,operation:'removed',before:a.get(id)});
      else if(JSON.stringify(a.get(id))!==JSON.stringify(b.get(id)))changes.push({collection,id,operation:'updated',before:a.get(id),after:b.get(id)});
    }
  }
  if(JSON.stringify(before.model.meta)!==JSON.stringify(after.model.meta))changes.push({collection:'meta',id:'meta',operation:'updated',before:before.model.meta,after:after.model.meta});
  return {schema_version:1,fromCursor:before.cursor,toCursor:after.cursor,fromRevision:before.revision,toRevision:after.revision,...paginate(changes,{from:[before.cursor,before.revision,before.evidenceRevision],to:[after.cursor,after.revision,after.evidenceRevision]},options)};
}
