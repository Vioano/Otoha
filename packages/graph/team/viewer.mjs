// Embedded into the standalone viewer by explorerHTML. Keep this function
// self-contained: it runs in the browser and never receives signing keys.
export function installOtohaTeam({ $, esc, snapshot, token, poll, renderDetails, t = value => value }) {
  let role=null, state=null, refreshing=false, draftVersion=null, request=null;
  const names=new Proxy({comment:'添加批注',label:'名称',purpose:'职责',inputs:'输入',outputs:'输出',steps:'关键步骤',openIssues:'未完成 / 局限',title:'标题',description:'说明',status:'进度',assignees:'负责人',entities:'关联模块',acceptance:'完成标准',deliverables:'产出与引用',blocked:'受阻原因'},{get:(values,key)=>t(values[key]||values.comment)});
  const arrays=new Set(['inputs','outputs','steps','openIssues']);
  const statusName=r=>!r?t('等待队长处理'):r.status==='accepted'?t('已生效'):t('已拒绝');
  const receiptHTML=r=>'<article class="request"><strong>'+esc(statusName(r))+'</strong><small>'+esc(r.actor)+' · '+esc(r.requestId)+' · #'+r.cursor+'</small><p>'+esc(r.changes.map(c=>(c.taskId||c.nodeId)+' / '+(names[c.field]||names.comment)).join('，'))+'</p>'+(r.code?'<p class="team-warning">'+esc(r.code)+' · '+esc(r.message)+'</p>':'')+'</article>';
  async function post(route,payload){const r=await fetch('/api/team/'+route,{method:'POST',headers:{'Content-Type':'application/json','X-Archify-Token':token()},body:JSON.stringify(payload)});const data=await r.json();if(!r.ok)throw Error(data.message||data.error);return data;}
  function nodeSection(id){
    if(!role)return '';
    const c=snapshot().collaboration;if(!c)return '';
    const people=c.members.filter(m=>m.grants.some(g=>g.nodes.includes(id)));
    return t('<section><h2>协作分工</h2>')+(people.length?people.map(m=>'<p>'+esc(m.actor)+(m.agentId?' · '+esc(m.agentId):'')+(m.sessionId?' · '+esc(m.sessionId):'')+'</p>').join(''):t('<p class="empty">尚未分配</p>'))+t('<small>分工与权限记录；不表示会话正在运行。</small></section><section><h2>批注</h2>')+c.comments.filter(c=>c.nodeId===id).map(c=>'<article class="request"><small>'+esc(c.actor)+' · '+esc(c.context?.agentId||'')+' · '+esc(c.context?.sessionId||'')+'</small><p>'+esc(c.text)+'</p></article>').join('')+'</section>';
  }
  function nodeReceipts(id){return (snapshot().collaboration?.receipts||[]).filter(r=>r.changes.some(c=>c.nodeId===id)).slice(-15).reverse().map(receiptHTML).join('')||t('<p class="empty">暂无协作变更</p>');}
  function render(){
    if(!state)return;
    $('team-heading').textContent=(role==='leader'?t('队长 · '):t('队员 · '))+state.actor;
    $('team-sync-status').textContent=(state.syncFailure?t('同步失败，保留已确认版本 · ')+state.syncFailure.message:state.lastSync?t('最近同步 ')+new Date(state.lastSync).toLocaleTimeString():t('尚未完成远端同步'))+t(' · 本机 #')+state.cursor;
    const outboxErrors=state.outboxErrors||[];
    if(outboxErrors.length)$('team-sync-status').textContent+=t(' · 请求文件待修复：')+outboxErrors.length;
    $('team-sync-status').classList.toggle('team-warning',!!state.syncFailure||!!outboxErrors.length);
    $('team-open').textContent=state.syncFailure||outboxErrors.length?t('协作 · 待同步'):t('协作');
    $('team-policy').innerHTML=state.members.map(m=>'<li><b>'+esc(m.actor)+'</b>'+m.grants.map(g=>' · '+esc(g.nodes.join(', '))+'：'+esc(g.fields.map(f=>names[f]).join('、')||t('无字段编辑'))+(g.comments?t('，可批注'):'')+(g.onlyIfEmpty?t('，仅限空字段'):'')).join('')+(m.taskGrants||[]).map(g=>' · '+t('任务')+': '+esc(g.tasks.join(', '))+' / '+esc(g.fields.map(f=>names[f]).join(', '))).join('')+(m.grants.length||m.taskGrants?.length?'':t(' · 已撤销写入权限'))+'</li>').join('')||t('<li>尚未授权成员</li>');
    $('team-receipts').innerHTML=(role==='member'?state.outbox.slice(-20).reverse().map(q=>q.receipt?receiptHTML(q.receipt):t('<article class="request"><strong>已排队 · 等待队长</strong><small>')+esc(q.requestId)+'</small><p>'+esc(q.changes.map(c=>(c.taskId||c.nodeId)+' / '+(names[c.field]||names.comment)).join('，'))+'</p></article>'):state.receipts.slice(-20).reverse().map(receiptHTML)).join('')||t('<p class="empty">暂无请求</p>');
    $('team-receipts').innerHTML=outboxErrors.map(e=>'<article class="request team-warning"><strong>'+esc(t('请求未上传，原文件保留'))+'</strong><small>'+esc(e.file)+'</small><p>'+esc(e.code)+' · '+esc(e.message)+'</p></article>').join('')+$('team-receipts').innerHTML;
    $('team-form').hidden=role!=='member';$('team-leader-note').hidden=role!=='leader';
    const receipt=request&&state.receipts.find(r=>r.actor===state.actor&&r.requestId===request.requestId);
    if(receipt)$('team-message').textContent=receipt.status==='accepted'?t('这个请求已生效。草稿保留；继续修改前，请重新载入已确认内容。'):t('这个请求已拒绝：')+receipt.message+t('。请核对权限和当前内容后再提交新请求。');
    if(role==='member'&&!$('team-node').options.length)loadNodes();
  }
  async function refresh(){
    if(!role||refreshing)return;refreshing=true;
    try{const r=await fetch('/api/team');if(!r.ok)throw Error(t('协作状态暂不可用'));state=await r.json();render();}
    catch(e){$('team-sync-status').textContent=e.message;}
    finally{refreshing=false;}
  }
  function loadNodes(){
    const s=snapshot(),member=s.collaboration?.members.find(m=>m.actor===state.actor),grants=member?.grants||[];
    const nodes=s.model.entities.filter(n=>grants.some(g=>g.nodes.includes(n.id)&&(g.fields.length||g.comments)));
    $('team-node').innerHTML=nodes.map(n=>'<option value="'+esc(n.id)+'">'+esc(n.label)+' · '+esc(n.id)+'</option>').join('');
    $('team-agent').value=member?.agentId||'';$('team-session').value=member?.sessionId||'';
    $('team-submit').disabled=!nodes.length;loadFields();
  }
  function loadFields(){
    const s=snapshot(),member=s.collaboration?.members.find(m=>m.actor===state.actor),grants=(member?.grants||[]).filter(g=>g.nodes.includes($('team-node').value));
    const fields=[...new Set(grants.flatMap(g=>[...g.fields,...(g.comments?['comment']:[])]))];
    $('team-field').innerHTML=fields.map(f=>'<option value="'+f+'">'+names[f]+'</option>').join('');loadValue();
  }
  function loadValue(){
    const s=snapshot(),node=s.model.entities.find(n=>n.id===$('team-node').value),field=$('team-field').value;
    draftVersion=s.collaboration?.fieldVersions[node?.id+':'+field];request=null;
    $('team-value').value=field==='comment'?'':arrays.has(field)?(node?.[field]||[]).join('\n'):node?.[field]||'';
    $('team-value-label').textContent=field==='comment'?t('批注内容'):arrays.has(field)?t('新内容（每行一项）'):t('新内容');
    $('team-draft-status').textContent=node?(field==='comment'?t('追加批注，保留已有内容。'):t('基于字段版本 #')+draftVersion+t('；等待期间不会自动覆盖草稿。')):t('目前没有可提交的节点权限。');
    $('team-message').textContent='';
  }
  function translate(){
    render();
    for(const option of $('team-field').options)option.textContent=names[option.value];
    const field=$('team-field').value,node=snapshot().model.entities.find(n=>n.id===$('team-node').value);
    $('team-value-label').textContent=field==='comment'?t('批注内容'):arrays.has(field)?t('新内容（每行一项）'):t('新内容');
    $('team-draft-status').textContent=node?(field==='comment'?t('追加批注，保留已有内容。'):t('基于字段版本 #')+draftVersion+t('；等待期间不会自动覆盖草稿。')):t('目前没有可提交的节点权限。');
  }
  $('team-open').onclick=async()=>{$('team-dialog').showModal();await refresh();};
  $('team-close').onclick=()=>$('team-dialog').close();
  $('team-node').onchange=loadFields;$('team-field').onchange=loadValue;
  $('team-reload').onclick=()=>{loadNodes();$('team-message').textContent=t('已载入当前权限和已确认内容。');};
  for(const id of ['team-value','team-agent','team-session'])$(id).oninput=()=>{request=null;$('team-message').textContent='';};
  $('team-submit').onclick=async()=>{
    const field=$('team-field').value,nodeId=$('team-node').value;if(!field||!nodeId)return;
    if(!request){const value=$('team-value').value;request={requestId:crypto.randomUUID(),context:{agentId:$('team-agent').value,sessionId:$('team-session').value},changes:[field==='comment'?{operation:'comment.add',nodeId,text:value}:{operation:'field.set',nodeId,field,expectedVersion:draftVersion,value:arrays.has(field)?value.split('\n').map(x=>x.trim()).filter(Boolean):value}]};}
    $('team-submit').disabled=true;
    try{await post('request',request);$('team-message').textContent=t('已保存并签名，等待同步与队长处理。图仍显示已确认版本。');await refresh();}
    catch(e){$('team-message').textContent=t('未确认保存：')+e.message+t('。重试会沿用请求 ID。');}
    finally{$('team-submit').disabled=false;}
  };
  $('team-sync').onclick=async()=>{
    $('team-sync').disabled=true;$('team-sync-status').textContent=t('正在同步…');
    try{await post('sync',{});await poll();await refresh();renderDetails();}
    catch(e){$('team-sync-status').textContent=t('同步未完成，保留已确认版本 · ')+e.message;}
    finally{$('team-sync').disabled=false;}
  };
  return {configure(value){role=value;$('team-open').hidden=!role;},active:()=>!!role,state:()=>state,role:()=>role,refresh,translate,nodeSection,nodeReceipts};
}
