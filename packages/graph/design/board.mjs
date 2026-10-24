// Serialized into the standalone explorer. No imports or hidden data authority.
export function installOtohaBoard({ root, snapshot, locale, connection, team, poll, navigate, project, detailPreference }) {
  const esc = x => String(x ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const dict = {
    Board:'任务看板', Canvas:'画布', 'Task board':'任务看板', 'All members':'所有成员', Unassigned:'未分配', 'Search tasks':'搜索任务', 'New task':'新建任务', 'To do':'待办', 'In progress':'进行中', Review:'待复核', Done:'已完成', 'No tasks':'暂无任务', 'No matches':'没有匹配任务', 'Task details':'任务详情', 'Locate in canvas':'定位到画布', 'Linked modules':'关联模块', Title:'标题', Description:'说明', Assignees:'负责人', Status:'进度', Acceptance:'完成标准', Deliverables:'产出与引用', Blocked:'受阻原因', 'One item per line':'每行一项', 'Names separated by commas':'姓名用逗号分隔', 'Save task':'保存任务', 'Send request':'提交请求', Close:'关闭', 'Discard draft & reload':'放弃草稿并重载', 'Reapply draft to latest':'将草稿应用到最新版本', 'Read-only snapshot':'只读快照', 'Waiting for leader':'等待队长处理', 'Saved':'已保存', 'Saving…':'保存中…', 'Not confirmed. Retry preserves the same request ID.':'结果未确认。重试会使用同一请求 ID。', 'Current accepted task':'当前已确认的任务', 'New version available; your draft is retained.':'已有新版本，草稿已保留。', 'Move cards between columns, or change Status in details.':'拖动卡片换列，或在详情中选择进度。', 'Task progress does not change module verification.':'任务进度不会改变模块验证状态。', 'Local preview required to edit.':'编辑需要启动本机预览。', 'Only the leader can create tasks.':'只有队长可以创建任务。', 'No editable task fields granted.':'尚未获得任务字段编辑权限。', 'No changes':'没有修改', 'Accepted':'已生效', 'Rejected':'已拒绝', 'Pending':'待处理', 'Select a linked module':'选择关联模块', 'Unsaved draft':'未保存草稿', 'Draft retained':'草稿已保留', 'No linked modules':'尚未关联模块', 'Clear filters':'清除筛选', 'Task data':'任务数据', 'Demo workspace · illustrative tasks and progress':'示例工作区 · 任务与进度仅用于演示'
  };
  Object.assign(dict, {'Filter':'筛选','Compact view':'精简视图','Float details':'浮动查看','Dock details':'固定到侧栏','Move details':'移动详情窗口','Delete task':'删除任务','Delete this task?':'删除这个任务？','Cancel':'取消','Delete':'删除','Undo deletion':'撤销删除','Task deleted':'任务已删除','Task restored':'任务已恢复','Only this task and its module links are removed. Canvas nodes stay unchanged. Undo restores the last saved task.':'只移除此任务及其模块关联，保留 Canvas 节点。撤销会恢复最后保存的任务。','Optional. Leave empty to keep this task independent of Canvas.':'可选。不勾选时，任务独立存在，不关联 Canvas 节点。','Drag to move; arrow keys to reposition':'拖动标题移动；方向键调整位置'});
  const t = value => locale() === 'zh-CN' ? dict[value] || value : value;
  const icons = { info:'<circle cx="12" cy="12" r="8"/><path d="M12 11v6m0-10v1"/>', locate:'<path d="M14 3h7v7m0-7L10 14M10 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-5"/>', plus:'<path d="M12 5v14M5 12h14"/>', close:'<path d="m6 6 12 12M6 18 18 6"/>', search:'<circle cx="10" cy="10" r="6"/><path d="m15 15 5 5"/>', code:'<path d="m8 7-5 5 5 5m8-10 5 5-5 5M14 4l-4 16"/>' };
  Object.assign(icons, {compact:'<path d="M4 5h16M4 12h16M4 19h16"/>',trash:'<path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7m4-7v7"/>',float:'<rect x="3" y="3" width="13" height="13" rx="2"/><rect x="8" y="8" width="13" height="13" rx="2"/>',dock:'<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M15 3v18"/>',grip:'<path d="M9 5h.01M15 5h.01M9 12h.01M15 12h.01M9 19h.01M15 19h.01" stroke-width="3"/>'});
  const tone = value => {let hash=0;for(const c of value)hash=(hash*31+c.codePointAt(0))>>>0;return hash%7;};
  const moduleTone = entity => {let e=entity;const seen=new Set();while(e?.parent&&!seen.has(e.id)){seen.add(e.id);e=model().entities.find(x=>x.id===e.parent)||e;}return tone(e?.id||'');};
  const icon = name => '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">'+icons[name]+'</svg>';
  const button = (action, name, glyph, extra='') => '<button type="button" class="board-icon" data-action="'+action+'" title="'+esc(t(name))+'" aria-label="'+esc(t(name))+'" '+extra+'>'+icon(glyph)+'</button>';
  const fields = ['title','status','assignees','entities','description','acceptance','deliverables','blocked'];
  const statuses = ['todo','doing','review','done'], names = ['To do','In progress','Review','Done'];
  let selected=null, draft=null, sending=false, message='', filters={assignee:'',search:'',filter:'all'}, dragging=null, renderKey='', actorKey='', filterKey='';
  const drafts=new Map();
  let compact=false;try{compact=localStorage.getItem('otoha-graph-board-compact')==='true';}catch{}
  root.dataset.density=compact?'compact':'comfortable';
  let deleteOperation=null, undoDeletion=null, restoreOperation=null;
  const model = () => snapshot().model;
  const task = id => model().tasks?.find(t=>t.id===id);
  const member = () => snapshot().collaboration?.members.find(m=>m.actor===team.state()?.actor);
  const writable = field => connection().online && (!team.active() || team.role()==='leader' || member()?.taskGrants?.some(g=>g.tasks.includes(draft?.id)&&g.fields.includes(field)));
  const canMove = id => connection().online && (!team.active() || team.role()==='leader' || member()?.taskGrants?.some(g=>g.tasks.includes(id)&&g.fields.includes('status')));
  const key = () => 'otoha-graph-task-drafts:'+model().meta.id+':'+(team.role()||'local')+':'+(team.state()?.actor||'');
  function persist(){try{localStorage.setItem(key(),JSON.stringify(Object.fromEntries(drafts)));}catch{}}
  root.innerHTML='<div class="board-toolbar"><div class="board-heading"><h1></h1><small class="board-count"></small></div><div class="board-filters"><label class="board-search">'+icon('search')+'<input type="search" maxlength="200"></label><select class="board-assignee"></select><details class="board-filter-menu"><summary>Filter</summary><select class="board-strategy" aria-label="Filter strategy"></select></details><button type="button" class="board-clear" data-action="clear"></button></div><div class="board-toolbar-actions">'+button('density','Compact view','compact')+button('data','Task data','code')+button('new','New task','plus')+'</div></div><div class="board-subtitle"></div><div class="board-message" role="status" aria-live="polite"></div><div class="board-workspace"><div class="board-columns"></div><div class="board-detail" role="region" hidden></div></div><dialog class="board-data"><div class="board-detail-tools"><strong></strong>'+button('data-close','Close','close')+'</div><pre></pre></dialog>';
  const columns=root.querySelector('.board-columns'), pane=root.querySelector('.board-detail'), search=root.querySelector('input[type=search]'), filter=root.querySelector('.board-assignee');
  root.insertAdjacentHTML('beforeend','<dialog class="board-delete-dialog"><h2></h2><strong class="board-delete-title"></strong><p></p><div class="board-delete-actions"><button type="button" data-action="delete-cancel"></button><button type="button" data-action="delete-confirm"></button></div></dialog>');
  root.querySelector('.board-message').insertAdjacentHTML('afterend','<div class="board-recovery" hidden><span></span><button type="button" data-action="undo-delete"></button></div>');
  const deleteDialog=root.querySelector('.board-delete-dialog'), recovery=root.querySelector('.board-recovery');
  const workspace=root.querySelector('.board-workspace');
  let detailMode='sidebar', floatingPosition=null, panelDrag=null;
  const editedCard=()=>columns.querySelector('[data-task="'+CSS.escape(draft?.id||'')+'"]');
  function positionPanel(position){
    const area=workspace.getBoundingClientRect(), bounds=pane.getBoundingClientRect();
    if(!area.width||!area.height)return;
    floatingPosition={x:Math.max(0,Math.min(area.width-bounds.width,position.x)),y:Math.max(0,Math.min(area.height-bounds.height,position.y))};
    pane.style.left=floatingPosition.x+'px';pane.style.top=floatingPosition.y+'px';
  }
  function placePanel(){
    if(pane.hidden||detailMode!=='floating')return;
    const area=workspace.getBoundingClientRect(), bounds=pane.getBoundingClientRect(), anchor=editedCard()?.getBoundingClientRect();
    if(!area.width||!area.height)return;
    if(floatingPosition){positionPanel(floatingPosition);return;}
    const leftSpace=anchor?anchor.left-area.left:0, rightSpace=anchor?area.right-anchor.right:0;
    positionPanel(anchor?{x:rightSpace>=bounds.width+12||rightSpace>=leftSpace?anchor.right-area.left+12:anchor.left-area.left-bounds.width-12,y:anchor.top-area.top}:{x:area.width-bounds.width,y:0});
  }
  function syncPanel(){
    detailMode=(detailPreference?.get()||detailMode)==='floating'?'floating':'sidebar';
    workspace.dataset.detailMode=detailMode;workspace.classList.toggle('has-detail',!pane.hidden);
    pane.setAttribute('aria-label',t('Task details'));
    const toggle=pane.querySelector('[data-action=detail-mode]'), grip=pane.querySelector('[data-action=move-detail]');
    if(toggle){const name=t(detailMode==='floating'?'Dock details':'Float details');toggle.title=name;toggle.setAttribute('aria-label',name);toggle.innerHTML=icon(detailMode==='floating'?'dock':'float');}
    if(grip){grip.hidden=detailMode!=='floating';grip.title=t('Drag to move; arrow keys to reposition');grip.setAttribute('aria-label',t('Move details'));}
    if(detailMode==='floating')placePanel();else{pane.style.removeProperty('left');pane.style.removeProperty('top');}
  }
  function revealCard(){
    const card=editedCard();if(!card)return;
    const rect=card.getBoundingClientRect(), area=columns.getBoundingClientRect();
    if(rect.left<area.left)columns.scrollLeft-=area.left-rect.left+10;
    else if(rect.right>area.right)columns.scrollLeft+=rect.right-area.right+10;
  }
  function togglePanel(){
    detailMode=detailMode==='floating'?'sidebar':'floating';
    detailPreference?.set(detailMode);floatingPosition=null;syncPanel();revealCard();placePanel();
  }
  // The inspector stays in the same document and keeps its form DOM when docking.
  new ResizeObserver(()=>{if(!root.hidden){if(detailMode==='sidebar')revealCard();else placePanel();}}).observe(workspace);
  pane.addEventListener('pointerdown',event=>{
    const header=event.target.closest('.board-detail-tools');
    if(detailMode!=='floating'||event.button!==0||!header||event.target.closest('button:not([data-action=move-detail])'))return;
    event.preventDefault();const rect=pane.getBoundingClientRect(), area=workspace.getBoundingClientRect();
    panelDrag={id:event.pointerId,x:event.clientX,y:event.clientY,left:rect.left-area.left,top:rect.top-area.top};
    header.setPointerCapture(event.pointerId);pane.classList.add('is-moving');
    pane.querySelector('[data-action=move-detail]').focus({preventScroll:true});
  });
  pane.addEventListener('pointermove',event=>{if(panelDrag?.id===event.pointerId)positionPanel({x:panelDrag.left+event.clientX-panelDrag.x,y:panelDrag.top+event.clientY-panelDrag.y});});
  for(const name of ['pointerup','pointercancel','lostpointercapture'])pane.addEventListener(name,()=>{panelDrag=null;pane.classList.remove('is-moving');});
  pane.addEventListener('keydown',event=>{
    if(detailMode!=='floating'||event.target.dataset.action!=='move-detail'||!['ArrowLeft','ArrowRight','ArrowUp','ArrowDown'].includes(event.key))return;
    event.preventDefault();const step=event.shiftKey?40:10;
    positionPanel({x:floatingPosition.x+({ArrowLeft:-step,ArrowRight:step}[event.key]||0),y:floatingPosition.y+({ArrowUp:-step,ArrowDown:step}[event.key]||0)});
  });
  function flash(text){message=text;root.querySelector('.board-message').textContent=text;}
  function storeDraft(){if(draft){drafts.set(draft.id,draft);persist();}}
  function patchOf(d){return Object.fromEntries(fields.filter(f=>JSON.stringify(d.values[f])!==JSON.stringify(d.original?.[f])).map(f=>[f,d.values[f]]));}
  function makeDraft(value,create=false){const s=snapshot();return {id:value.id,create,values:structuredClone(value),original:create?null:structuredClone(value),cursor:s.cursor,versions:structuredClone(s.collaboration?.fieldVersions||{}),operation:null,conflict:false};}
  function open(id,create=false){
    storeDraft();const value=create?{id:'task-'+crypto.randomUUID(),title:'',status:'todo',assignees:filters.assignee&&filters.assignee!=='__unassigned__'?[filters.assignee]:[],entities:[],description:'',acceptance:[],deliverables:[],blocked:''}:task(id);
    if(!value)return;const retained=create?[...drafts.values()].find(d=>d.create):drafts.get(value.id);selected=retained?.id||value.id;draft=retained||makeDraft(value,create);floatingPosition=null;storeDraft();renderEditor();update(true);revealCard();placePanel();pane.querySelector('input')?.focus({preventScroll:true});
  }
  function close(){storeDraft();draft=null;pane.hidden=true;update(true);columns.querySelector('[data-task="'+CSS.escape(selected||'')+'"]')?.focus();}
  function labelField(field,label,control){const tag=field==='entities'?'div':'label';return '<'+tag+' class="board-field" data-field="'+field+'"><span>'+esc(t(label))+'</span>'+control+'</'+tag+'>';}
  function renderEditor(){
    if(!draft){pane.hidden=true;return;}pane.hidden=false;const d=draft, v=d.values;
    const input=(f,label,kind='input',hint='')=>labelField(f,label,kind==='input'?'<input name="'+f+'" value="'+esc(v[f])+'" '+(f==='title'?'maxlength="200" required':'maxlength="2000"')+' '+(!writable(f)?'disabled':'')+'>':'<textarea name="'+f+'" rows="'+(f==='description'?4:3)+'" '+(!writable(f)?'disabled':'')+' placeholder="'+esc(t(hint))+'">'+esc(Array.isArray(v[f])?v[f].join('\n'):v[f])+'</textarea>');
    pane.innerHTML='<div class="board-detail-tools">'+button('move-detail','Move details','grip')+'<div><small>'+esc(d.create?t('New task'):d.id)+'</small><h2>'+esc(t('Task details'))+'</h2></div>'+button('detail-mode','Float details','float')+button('close','Close','close')+'</div><form class="board-editor">'+input('title','Title')+'<div class="board-field-pair">'+labelField('status','Status','<select name="status" '+(!writable('status')?'disabled':'')+'>'+statuses.map((s,i)=>'<option value="'+s+'" '+(v.status===s?'selected':'')+'>'+esc(t(names[i]))+'</option>').join('')+'</select>')+labelField('assignees','Assignees','<input name="assignees" maxlength="2000" placeholder="'+esc(t('Names separated by commas'))+'" value="'+esc(v.assignees.join(', '))+'" '+(!writable('assignees')?'disabled':'')+'>')+'</div>'+labelField('entities','Linked modules','<div class="board-module-choices">'+model().entities.map(e=>'<label><input type="checkbox" name="entities" aria-label="'+esc(e.label)+'" value="'+esc(e.id)+'" '+(v.entities.includes(e.id)?'checked ':'')+(!writable('entities')?'disabled':'')+'><span>'+esc(e.label)+'</span>'+button('locate','Locate in canvas','locate','data-entity="'+esc(e.id)+'"')+'</label>').join('')+'</div><small class="board-link-help">'+esc(t('Optional. Leave empty to keep this task independent of Canvas.'))+'</small>')+input('description','Description','textarea')+input('acceptance','Acceptance','textarea','One item per line')+input('deliverables','Deliverables','textarea','One item per line')+input('blocked','Blocked','textarea')+'<p class="board-draft-state"></p><div class="board-conflict" hidden><strong>'+esc(t('Current accepted task'))+'</strong><pre></pre><button type="button" data-action="reapply">'+esc(t('Reapply draft to latest'))+'</button></div><div class="board-editor-footer"><button type="submit" class="board-save">'+esc(t(team.role()==='member'?'Send request':'Save task'))+'</button><button type="button" data-action="reload">'+esc(t('Discard draft & reload'))+'</button></div><p class="board-permission-note"></p>'+(!d.create&&team.role()!=='member'?'<div class="board-danger-zone">'+button('delete','Delete task','trash')+'</div>':'')+'</form>';
    const form=pane.querySelector('form');form.onsubmit=event=>{event.preventDefault();save();};
    form.oninput=()=>{readForm();d.operation=null;d.conflict=false;storeDraft();refreshDraftState();};
    syncPanel();refreshDraftState();
  }
  function readForm(){if(!draft)return;const form=pane.querySelector('form');for(const f of fields){if(!writable(f))continue;if(f==='entities')draft.values[f]=[...form.querySelectorAll('[name=entities]:checked')].map(i=>i.value);else{const value=form.elements.namedItem(f).value;draft.values[f]=['acceptance','deliverables'].includes(f)?[...new Set(value.split('\n').map(s=>s.trim()).filter(Boolean))]:f==='assignees'?[...new Set(value.split(/[,，]/).map(s=>s.trim()).filter(Boolean))]:value;}}}
  function refreshDraftState(){
    if(!draft)return;const stale=draft.cursor!==snapshot().cursor, changed=draft.create||Object.keys(patchOf(draft)).length;
    pane.querySelector('.board-draft-state').textContent=(stale?t('New version available; your draft is retained.'):changed?t('Unsaved draft'):t('Accepted'))+' · #'+draft.cursor;
    const conflict=pane.querySelector('.board-conflict');conflict.hidden=!draft.conflict;
    if(draft.conflict)conflict.querySelector('pre').textContent=JSON.stringify(task(draft.id)||null,null,2);
    for(const control of pane.querySelectorAll('[data-field] :is(input,select,textarea)'))control.disabled=sending||!writable(control.closest('[data-field]').dataset.field);
    const permitted=fields.some(writable), submit=pane.querySelector('[type=submit]');submit.disabled=sending||!permitted||!changed||draft.conflict;
    submit.textContent=t(sending?'Saving…':team.role()==='member'?'Send request':'Save task');
    pane.querySelector('.board-permission-note').textContent=!connection().online?t('Local preview required to edit.'):!permitted?t('No editable task fields granted.'):t('Task progress does not change module verification.');
    pane.querySelector('[data-action=reload]').disabled=sending;
    const remove=pane.querySelector('[data-action=delete]');if(remove)remove.disabled=sending||!connection().online;
  }
  async function send(d){
    if(team.role()==='member'){
      if(d.create)throw Error(t('Only the leader can create tasks.'));
      if(!d.operation)d.operation={route:'/api/team/request',payload:{requestId:crypto.randomUUID(),context:{agentId:member()?.agentId||'',sessionId:member()?.sessionId||''},changes:Object.entries(patchOf(d)).map(([field,value])=>({operation:'task.set',taskId:d.id,field,value,expectedVersion:d.versions['task:'+d.id+':'+field]}))}};
    }else if(!d.operation)d.operation={route:'/api/tasks',payload:{operationId:crypto.randomUUID(),expectedCursor:d.cursor,...(d.create?{action:'create',task:d.values}:{action:'update',taskId:d.id,patch:patchOf(d)})}};
    storeDraft();const {route,payload}=d.operation;
    const response=await fetch(route,{method:'POST',headers:{'Content-Type':'application/json','X-Archify-Token':connection().token},body:JSON.stringify(payload)});
    const data=await response.json();if(!response.ok){const error=Error(data.message||data.error);error.status=response.status;throw error;}return data;
  }
  async function save(){
    if(!draft||sending)return;readForm();if(!Object.keys(patchOf(draft)).length){flash(t('No changes'));return;}
    const d=draft;sending=true;refreshDraftState();flash(t('Saving…'));
    try{await send(d);drafts.delete(d.id);persist();if(draft===d){draft=null;pane.hidden=true;}flash(t(team.role()==='member'?'Waiting for leader':'Saved'));await poll();update(true);}
    catch(error){if(error.status===409){d.conflict=true;d.operation=null;await poll();}flash(error.status?error.message:t('Not confirmed. Retry preserves the same request ID.'));storeDraft();}
    finally{sending=false;refreshDraftState();}
  }
  async function move(id,status,baseline){
    const current=task(id);if(!current||current.status===status||sending||!canMove(id))return;
    if(drafts.has(id)&&Object.keys(patchOf(drafts.get(id))).length){open(id);flash(t('Draft retained'));return;}
    const d=baseline||makeDraft(current);d.values.status=status;sending=true;
    try{await send(d);flash(t(team.role()==='member'?'Waiting for leader':'Saved'));await poll();}
    catch(error){d.conflict=error.status===409;drafts.set(id,d);draft=d;selected=id;storeDraft();renderEditor();flash(error.status?error.message:t('Not confirmed. Retry preserves the same request ID.'));}
    finally{sending=false;update(true);refreshDraftState();}
  }
  // Deletion and undo are authority commands, never browser-only card removal.
  function confirmDelete(){
    if(!draft||draft.create||sending||!connection().online||team.role()==='member')return;
    storeDraft();deleteOperation={operationId:crypto.randomUUID(),expectedCursor:snapshot().cursor,action:'delete',taskId:draft.id};
    deleteDialog.querySelector('h2').textContent=t('Delete this task?');
    deleteDialog.querySelector('strong').textContent=task(draft.id)?.title||draft.values.title;
    deleteDialog.querySelector('p').textContent=t('Only this task and its module links are removed. Canvas nodes stay unchanged. Undo restores the last saved task.');
    deleteDialog.querySelector('[data-action=delete-cancel]').textContent=t('Cancel');
    deleteDialog.querySelector('[data-action=delete-confirm]').textContent=t('Delete');deleteDialog.showModal();
  }
  async function lifecycleCommand(payload){
    const response=await fetch('/api/tasks',{method:'POST',headers:{'Content-Type':'application/json','X-Archify-Token':connection().token},body:JSON.stringify(payload)});
    const data=await response.json();if(!response.ok){const error=Error(data.message||data.error);error.status=response.status;throw error;}return data;
  }
  async function deleteTask(){
    if(!deleteOperation||sending)return;const operation=deleteOperation, title=task(operation.taskId)?.title||draft?.values.title||operation.taskId;
    sending=true;refreshDraftState();for(const b of deleteDialog.querySelectorAll('button'))b.disabled=true;
    try{await lifecycleCommand(operation);undoDeletion={id:operation.operationId,title};restoreOperation=null;
      drafts.delete(operation.taskId);persist();if(draft?.id===operation.taskId){draft=null;pane.hidden=true;}deleteDialog.close();deleteOperation=null;
      await poll();flash(t('Task deleted'));update(true);recovery.querySelector('button').focus({preventScroll:true});
    }catch(error){flash(error.status?error.message:t('Not confirmed. Retry preserves the same request ID.'));if(error.status){deleteDialog.close();deleteOperation=null;await poll();}}
    finally{sending=false;for(const b of deleteDialog.querySelectorAll('button'))b.disabled=false;update(true);refreshDraftState();}
  }
  async function restoreTask(){
    if(!undoDeletion||sending)return;
    restoreOperation ||= {operationId:crypto.randomUUID(),expectedCursor:snapshot().cursor,action:'restore',deletionId:undoDeletion.id};
    sending=true;update(true);
    try{await lifecycleCommand(restoreOperation);undoDeletion=null;restoreOperation=null;recovery.hidden=true;flash(t('Task restored'));await poll();update(true);}
    catch(error){if(error.status){restoreOperation=null;await poll();}flash(error.status?error.message:t('Not confirmed. Retry preserves the same request ID.'));}
    finally{sending=false;update(true);}
  }
  function update(force=false){
    const s=snapshot(), state=team.state();
    if(actorKey!==key()){actorKey=key();drafts.clear();draft=null;pane.hidden=true;undoDeletion=null;restoreOperation=null;recovery.hidden=true;try{const saved=JSON.parse(localStorage.getItem(actorKey)||'{}');for(const [id,d] of Object.entries(saved))if(d?.values&&d?.id===id)drafts.set(id,d);}catch{}}
    syncPanel();
    if(undoDeletion){recovery.hidden=false;recovery.querySelector('span').textContent=t('Task deleted')+' · '+undoDeletion.title;const undo=recovery.querySelector('button');undo.textContent=t('Undo deletion');undo.disabled=sending||!connection().online||team.role()==='member';}
    const signature=JSON.stringify([s.cursor,s.revision,locale(),filters,selected,!!draft,connection().online,team.role(),state?.actor,state?.outbox]);
    if(dragging){refreshDraftState();return;}
    if(!force&&signature===renderKey){refreshDraftState();return;}renderKey=signature;
    root.querySelector('h1').textContent=t('Task board');root.querySelector('.board-subtitle').textContent=(model().meta.demo?t('Demo workspace · illustrative tasks and progress')+' · ':'')+t('Move cards between columns, or change Status in details.');
    const strategy=root.querySelector('.board-strategy');strategy.innerHTML=otohaFilterPresets('board').map(([id,en,zh])=>'<option value="'+id+'">'+esc(locale()==='zh-CN'?zh:en)+'</option>').join('');strategy.value=filters.filter;strategy.setAttribute('aria-label',t('Filter'));const summary=root.querySelector('.board-filter-menu summary');summary.textContent=t('Filter')+(filters.filter==='all'?'':' · '+strategy.selectedOptions[0].textContent);summary.title=summary.textContent;
    search.placeholder=t('Search tasks');search.setAttribute('aria-label',t('Search tasks'));filter.setAttribute('aria-label',t('Assignees'));
    const members=[...new Set((model().tasks||[]).flatMap(t=>t.assignees))].sort();filter.innerHTML='<option value="">'+esc(t('All members'))+'</option><option value="__unassigned__">'+esc(t('Unassigned'))+'</option>'+members.map(m=>'<option>'+esc(m)+'</option>').join('');filter.value=filters.assignee;
    root.querySelector('.board-clear').textContent=t('Clear filters');root.querySelector('.board-clear').disabled=!filters.assignee&&!filters.search&&filters.filter==='all';
    for(const [action,name] of [['new','New task'],['data','Task data'],['density','Compact view']]){const b=root.querySelector('[data-action='+action+']');b.title=t(name);b.setAttribute('aria-label',t(name));}
    root.querySelector('[data-action=density]').setAttribute('aria-pressed',String(compact));
    root.querySelector('[data-action=new]').disabled=!connection().online||team.role()==='member';
    const projection=project(model(),filters), tasksById=new Map(projection.tasks.map(value=>[value.id,value])), nextFilterKey=JSON.stringify(filters), filtersChanged=nextFilterKey!==filterKey;
    filterKey=nextFilterKey;
    const previousScroll=columns.scrollLeft, scrolls=new Map([...columns.querySelectorAll('.board-lane')].map(l=>[l.dataset.status,l.scrollTop]));
    const focused=document.activeElement?.closest?.('[data-task]')?.dataset.task, action=document.activeElement?.dataset?.action;
    const count=root.querySelector('.board-count'), total=(model().tasks||[]).length;
    count.textContent=projection.tasks.length+' / '+total;count.style.minWidth=(String(total).length*2+3)+'ch';
    columns.innerHTML=projection.columns.map((column,index)=>'<section class="board-column" data-status="'+column.status+'"><div class="board-column-heading"><span class="board-dot"></span><h2>'+esc(t(names[index]))+'</h2><span>'+column.taskIds.length+'</span></div><div class="board-lane" data-status="'+column.status+'">'+column.taskIds.map(id=>{
      const value=tasksById.get(id), related=value.entities.map(id=>model().entities.find(e=>e.id===id));
      const pending=state?.outbox?.filter(r=>!r.receipt&&r.changes.some(c=>c.taskId===id)).length;
      return '<article class="task-card '+(selected===id?'is-selected':'')+'" tabindex="0" data-task="'+esc(id)+'" draggable="'+canMove(id)+'" aria-label="'+esc(value.title)+'"><div class="task-card-top"><span class="task-id">'+esc(id)+'</span>'+(value.blocked?'<span class="task-blocked-flag" title="'+esc(value.blocked)+'">'+esc(t('Blocked'))+'</span>':'')+button('inspect','Task details','info')+'</div><h3>'+esc(value.title)+'</h3>'+(value.description?'<p class="task-description">'+esc(value.description)+'</p>':'')+(value.blocked?'<div class="task-blocked">'+esc(t('Blocked'))+' · '+esc(value.blocked)+'</div>':'')+'<div class="task-module-chips">'+related.slice(0,2).map(e=>'<span data-tone="'+moduleTone(e)+'">'+esc(e?.label)+'</span>').join('')+(related.length>2?'<span>+'+(related.length-2)+'</span>':'')+'</div><div class="task-card-footer"><div class="task-people">'+(value.assignees.length?value.assignees.map(a=>'<span class="task-person" data-tone="'+tone(a.toLowerCase())+'" title="'+esc(a)+'" aria-label="'+esc(a)+'">'+esc([...a].slice(0,2).join(''))+'</span>').join(''):'<small>'+esc(t('Unassigned'))+'</small>')+'</div>'+(pending?'<small class="task-pending">'+esc(t('Pending'))+'</small>':'')+button('locate','Locate in canvas','locate',related.length?'':'disabled')+'</div></article>';
    }).join('')+(column.taskIds.length?'':'<div class="board-empty">'+esc(t(filters.assignee||filters.search||filters.filter!=='all'?'No matches':'No tasks'))+'</div>')+'</div></section>').join('');
    columns.scrollLeft=previousScroll;columns.querySelectorAll('.board-lane').forEach(l=>l.scrollTop=filtersChanged?0:scrolls.get(l.dataset.status)||0);
    if(focused){const card=columns.querySelector('[data-task="'+CSS.escape(focused)+'"]');(action?card?.querySelector('[data-action='+action+']'):card)?.focus({preventScroll:true});}
    root.dataset.cursor=String(s.cursor||'offline');root.dataset.revision=s.revision;refreshDraftState();
  }
  root.addEventListener('click',event=>{
    const b=event.target.closest('[data-action]'), card=event.target.closest('[data-task]');
    if(!b){if(card){selected=card.dataset.task;update(true);}return;}
    const action=b.dataset.action,id=card?.dataset.task;
    if(action==='density'){
      // Keep the first visible task in each lane when card heights change.
      const anchors=[...columns.querySelectorAll('.board-lane')].map(l=>{const top=l.getBoundingClientRect().top,card=[...l.querySelectorAll('.task-card')].find(c=>c.getBoundingClientRect().bottom>top);return card?{status:l.dataset.status,id:card.dataset.task,offset:card.getBoundingClientRect().top-top}:null;});
      compact=!compact;root.dataset.density=compact?'compact':'comfortable';try{localStorage.setItem('otoha-graph-board-compact',String(compact));}catch{}update(true);
      for(const a of anchors){if(!a)continue;const lane=columns.querySelector('.board-lane[data-status='+a.status+']'),card=lane?.querySelector('[data-task="'+CSS.escape(a.id)+'"]');if(card){const box=card.getBoundingClientRect();lane.scrollTop+=box.top-lane.getBoundingClientRect().top-Math.max(1-box.height,a.offset);}}
    }
    else if(action==='inspect')open(id);
    else if(action==='new')open(null,true);
    else if(action==='close')close();
    else if(action==='detail-mode')togglePanel();
    else if(action==='delete')confirmDelete();
    else if(action==='delete-cancel')deleteDialog.close();
    else if(action==='delete-confirm')deleteTask();
    else if(action==='undo-delete')restoreTask();
    else if(action==='clear'){filters={assignee:'',search:'',filter:'all'};search.value='';update(true);}
    else if(action==='locate'){
      const value=task(id||draft?.id), entity=b.dataset.entity||value?.entities[0];
      if(!b.dataset.entity&&value?.entities.length>1){open(value.id);flash(t('Select a linked module'));pane.querySelector('.board-module-choices').scrollIntoView({block:'nearest'});return;}
      if(entity){storeDraft();navigate(entity);}
    }else if(action==='reload'){const id=draft.id,create=draft.create;drafts.delete(id);draft=null;persist();create?close():open(id);}
    else if(action==='reapply'){
      const current=task(draft.id);if(!current||draft.create){flash(t('Discard draft & reload'));return;}
      const patch=patchOf(draft);draft=makeDraft({...current});Object.assign(draft.values,patch);storeDraft();renderEditor();
    }else if(action==='data'){
      const dialog=root.querySelector('.board-data');dialog.querySelector('strong').textContent=t('Task data');dialog.querySelector('pre').textContent=JSON.stringify({cursor:snapshot().cursor,revision:snapshot().revision,query:{mode:'board',...filters},...project(model(),filters)},null,2);dialog.showModal();
    }else if(action==='data-close')root.querySelector('.board-data').close();
  });
  root.addEventListener('keydown',event=>{if(event.target.matches('.task-card')&&['Enter',' '].includes(event.key)){event.preventDefault();selected=event.target.dataset.task;update(true);}if(event.key==='Escape'&&draft&&!event.target.closest('dialog')){event.preventDefault();close();}});
  root.querySelector('.board-strategy').onchange=event=>{filters.filter=event.target.value;update(true);};
  search.oninput=()=>{filters.search=search.value;update(true);};filter.onchange=()=>{filters.assignee=filter.value;update(true);};
  columns.addEventListener('dragstart',event=>{const card=event.target.closest('[data-task]');if(!card||event.target.closest('button')||!canMove(card.dataset.task)){event.preventDefault();return;}dragging=makeDraft(task(card.dataset.task));event.dataTransfer.effectAllowed='move';event.dataTransfer.setData('text/plain',dragging.id);card.classList.add('is-dragging');});
  columns.addEventListener('dragover',event=>{if(!dragging)return;const lane=event.target.closest('.board-column');if(lane){event.preventDefault();event.dataTransfer.dropEffect='move';columns.querySelectorAll('.is-drop-target').forEach(e=>e.classList.remove('is-drop-target'));lane.classList.add('is-drop-target');}});
  columns.addEventListener('drop',event=>{const lane=event.target.closest('.board-column');if(!dragging||!lane)return;event.preventDefault();const baseline=dragging;dragging=null;columns.querySelectorAll('.is-drop-target').forEach(e=>e.classList.remove('is-drop-target'));move(baseline.id,lane.dataset.status,baseline);});
  columns.addEventListener('dragend',()=>{dragging=null;columns.querySelectorAll('.is-drop-target,.is-dragging').forEach(e=>e.classList.remove('is-drop-target','is-dragging'));update();});
  return { update, translate(){renderKey='';if(draft)renderEditor();update(true);}, persist:storeDraft };
}
