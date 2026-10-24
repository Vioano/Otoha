// Pure reading strategies shared by the browser and Agent query interface.
// No authority writes, task dispatch, or inference from screen positions.
export function otohaFilterPresets(scope) {
  return scope==='board' ? [
    ['all','All tasks','全部任务'],['active','Unfinished','未完成'],['blocked','Blocked','受阻'],['review','Awaiting review','待复核'],['unassigned','Unassigned','未分配']
  ] : [
    ['all','All nodes','全部节点'],['unverified','Verification not passed','验证未通过'],['blocked','Linked to blocked tasks','受阻任务关联模块'],['neighbors','Selected + neighbors','选中节点及邻接'],['upstream','Selected + upstream','选中节点及上游'],['downstream','Selected + downstream','选中节点及下游']
  ];
}
export function matchesTaskFilter(task, strategy='all') {
  if(strategy==='all')return true;
  if(strategy==='active')return task.status!=='done';
  if(strategy==='blocked')return !!task.blocked.trim();
  if(strategy==='review')return task.status==='review';
  if(strategy==='unassigned')return !task.assignees.length;
  throw Error('Unknown task filter: '+strategy);
}
export function filterGraphEntities(model, ids, strategy='all', target) {
  if(!otohaFilterPresets('canvas').some(([id])=>id===strategy))throw Error('Unknown graph filter: '+strategy);
  const available=new Set(ids),byId=new Map(model.entities.map(e=>[e.id,e]));let matches;
  if(['neighbors','upstream','downstream'].includes(strategy)){
    if(!byId.has(target))throw Error('Select an existing target node for '+strategy);
    const adjacency=new Map(model.entities.map(e=>[e.id,[]]));
    for(const e of model.relations){if(strategy!=='upstream')adjacency.get(e.from).push(e.to);if(strategy!=='downstream')adjacency.get(e.to).push(e.from);}
    matches=new Set([target]);const queue=[target];
    for(let i=0;i<queue.length;i++){for(const id of adjacency.get(queue[i])||[])if(!matches.has(id)){matches.add(id);if(strategy!=='neighbors')queue.push(id);}}
  }else if(strategy==='unverified')matches=new Set(model.entities.filter(e=>(e.maturity.verification.effective||e.maturity.verification.value)!=='passed').map(e=>e.id));
  else if(strategy==='blocked')matches=new Set((model.tasks||[]).filter(t=>t.blocked.trim()).flatMap(t=>t.entities));
  else matches=new Set(model.entities.map(e=>e.id));
  const matchedIds=[...available].filter(id=>matches.has(id)),kept=new Set(matchedIds),context=new Set();
  // An expanded child needs its visible containment frame. Context is not a match.
  for(const id of matchedIds){let e=byId.get(id);const seen=new Set();while(e?.parent&&!seen.has(e.parent)){seen.add(e.parent);if(available.has(e.parent)&&!matches.has(e.parent)){kept.add(e.parent);context.add(e.parent);}e=byId.get(e.parent);}}
  return {ids:[...kept],matchedIds,contextIds:[...context],hiddenCount:available.size-kept.size,matchingOutsideView:[...matches].filter(id=>!available.has(id)).length};
}
