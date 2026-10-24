// Optional real-browser regression. No production data, keys or server are used.
// node scripts/test-board-browser.mjs [absolute Playwright index.mjs] [output-dir]
// Uses installed Chrome; install/provide Playwright separately; it is not a runtime dependency.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
import {fixture} from '../test/helpers/system-fixture.mjs';
import {startDesignPreview} from '../design/server.mjs';
const {chromium}=await import(process.argv[2]?pathToFileURL(path.resolve(process.argv[2])).href:'playwright');
const output=process.argv[3]||fs.mkdtempSync(path.join(os.tmpdir(),'otoha-board-browser-'));
fs.mkdirSync(output,{recursive:true});
const f=fixture(),errors=[],checks=[],metrics=[];
const makeTasks=n=>Array.from({length:n},(_,i)=>({id:`stress-${String(i).padStart(4,'0')}`,title:i===0?'长标题'.repeat(40):`Synthetic task ${i}`,description:'Synthetic fixture; no user data. '.repeat(4),status:['todo','doing','review','done'][i%4],assignees:[i%3?'alice':'bob'],entities:[],acceptance:['fixture only'],deliverables:[],blocked:i===0?'x'.repeat(1000):''}));
f.model.meta.title='Board stress fixture — synthetic';f.model.tasks=makeTasks(600);f.model.tasks[0].entities=['decode'];f.model.tasks[599].assignees=[];fs.writeFileSync(f.input,JSON.stringify(f.model));
let server,browser;
try{
 server=await startDesignPreview({...f.options,pollMs:1000});browser=await chromium.launch({headless:true,channel:'chrome'});
 const page=await browser.newPage({viewport:{width:1440,height:900}});page.on('pageerror',e=>errors.push(e.message));
 const start=performance.now();await page.goto(server.url+'#surface=board');await page.locator('.task-card').last().waitFor({state:'attached'});
 metrics.push({case:'600 first render',milliseconds:Math.round(performance.now()-start)});
 const lanes=page.locator('.board-lane');
 const geometry=()=>page.evaluate(()=>({bodyWidth:document.body.scrollWidth,viewport:innerWidth,lanes:[...document.querySelectorAll('.board-lane')].map(e=>({width:e.clientWidth,scrollWidth:e.scrollWidth,height:e.clientHeight,scrollHeight:e.scrollHeight,scrollTop:e.scrollTop})),headings:[...document.querySelectorAll('.board-column-heading')].map(e=>e.getBoundingClientRect().y)}));
 assert.equal(await page.locator('.task-card').count(),600);
 let before=await geometry();assert.equal(before.bodyWidth,before.viewport);for(const lane of before.lanes){assert.ok(lane.height>100);assert.ok(lane.scrollHeight>lane.height);assert.ok(lane.scrollWidth<=lane.width+1,JSON.stringify(lane));}
 checks.push('600 cards; bounded lane widths/heights; no text overflow');
 // A stable default screenshot is insufficient: compare every control across states.
 const toolbarSelectors=['.board-search','.board-assignee','.board-filter-menu summary','.board-clear','.board-toolbar-actions','.board-columns'];
 const toolbarGeometry=()=>page.evaluate(selectors=>Object.fromEntries(selectors.map(s=>{
  const r=document.querySelector('#task-board '+s).getBoundingClientRect();
  return [s,{x:r.x,y:r.y,width:r.width,height:r.height}];
 })),toolbarSelectors);
 const assertStable=async(baseline,label)=>{
  const actual=await toolbarGeometry();
  for(const s of toolbarSelectors)for(const key of ['x','y','width','height'])
   assert.ok(Math.abs(actual[s][key]-baseline[s][key])<=.5,`${label}: ${s} ${key} moved`);
  assert.ok((await geometry()).bodyWidth<=(await geometry()).viewport+1,'toolbar causes page overflow');
 };
 for(const width of [1440,1157,1024,760,390]){
  await page.setViewportSize({width,height:844});const baseline=await toolbarGeometry();
  assert.equal(await page.locator('#task-board .board-clear').isEnabled(),false);
  await page.locator('#task-board .board-filter-menu summary').click();await assertStable(baseline,`${width} menu open`);
  for(const filter of ['blocked','review','unassigned','active','all']){
   await page.locator('.board-strategy').selectOption(filter);await assertStable(baseline,`${width} ${filter}`);
  }
  await page.locator('#task-board .board-filter-menu summary').click();
  await page.locator('.board-assignee').selectOption('alice');await page.locator('.board-search input').fill('no-such-task');
  await assertStable(baseline,`${width} member and empty search`);
  await page.locator('#task-board .board-clear').click();await assertStable(baseline,`${width} clear`);
 }
 checks.push('toolbar geometry stable across presets, member/search empty result and clear at five widths');
 await page.setViewportSize({width:1440,height:900});

 await page.screenshot({path:path.join(output,'desktop.png')});
 await lanes.evaluateAll(es=>es.forEach(e=>e.scrollTop=e.scrollHeight));
 const scrolled=await geometry();assert.deepEqual(scrolled.headings,before.headings);
 for(let i=0;i<4;i++){const last=lanes.nth(i).locator('.task-card').last();assert.ok(await last.isVisible());const r=await last.boundingBox(),l=await lanes.nth(i).boundingBox();assert.ok(r.y+r.height<=l.y+l.height+1);}
 checks.push('last card reachable in all four lanes; headings stay fixed');
 // An accepted update away from the selected card keeps independent scroll offsets.
 const cursor=server.authority.record().cursor;const model=JSON.parse(fs.readFileSync(f.input));model.tasks[1].title='Accepted update at top';fs.writeFileSync(f.input,JSON.stringify(model));server.authority.refresh();
 await page.waitForFunction(c=>Number(document.querySelector('#task-board').dataset.cursor)>c,cursor);
 let after=await geometry();assert.deepEqual(after.lanes.map(l=>l.scrollTop),scrolled.lanes.map(l=>l.scrollTop));checks.push('accepted update preserves lane scroll positions');
 await page.locator('.board-assignee').selectOption('bob');assert.equal(await page.locator('.task-card').count(),200);assert.ok((await geometry()).lanes.every(l=>l.scrollTop===0));
 await page.locator('.board-search input').fill('not-a-real-task');assert.equal(await page.locator('.task-card').count(),0);assert.equal(await page.locator('.board-empty').count(),4);
 await page.locator('[data-action=clear]').click();assert.equal(await page.locator('.task-card').count(),600);checks.push('member/search/empty/clear; filter change resets lane positions');
 await page.locator('[data-task=stress-0000] [data-action=inspect]').click();assert.equal(await page.locator('[name=title]').inputValue(),'长标题'.repeat(40));assert.equal(await page.locator('[name=blocked]').inputValue(),'x'.repeat(1000));await page.locator('.board-detail [data-action=close]').click();checks.push('clamped text stays complete in editor');
 // Presets intersect existing member/search filters; compare the exact Agent projection.
 await page.locator('#task-board .board-filter-menu summary').click();
 for(const [filter,count] of [['active',450],['blocked',1],['review',150],['unassigned',1],['all',600]]){
  await page.locator('.board-strategy').selectOption(filter);assert.equal(await page.locator('.task-card').count(),count);
  const q=server.authority.query({mode:'board',filter,limit:1000,maxBytes:1000000});assert.deepEqual(await page.locator('.task-card').evaluateAll(es=>es.map(e=>e.dataset.task).sort()),q.records.map(r=>r.value.id).sort());
 }
 await page.locator('#task-board .board-filter-menu summary').click();checks.push('all five Board presets match Agent IDs');
 // Density changes keep visible task anchors and editor drafts, and persist locally.
 await lanes.evaluateAll(es=>es.forEach(e=>e.scrollTop=1500));
 const anchors=()=>page.locator('.board-lane').evaluateAll(es=>es.map(e=>[...e.querySelectorAll('.task-card')].find(c=>c.getBoundingClientRect().bottom>e.getBoundingClientRect().top)?.dataset.task));
 const visibleBefore=await anchors();await page.locator('[data-action=density]').click();assert.deepEqual(await anchors(),visibleBefore);
 assert.deepEqual(await page.locator('.task-card').evaluateAll(es=>[...new Set(es.map(e=>e.getBoundingClientRect().height))]),[94]);
 await lanes.evaluateAll(es=>es.forEach(e=>e.scrollTop=0));assert.ok(await page.locator('.task-blocked-flag').first().isVisible());
 await page.locator('[data-task=stress-0000] [data-action=inspect]').click();await page.locator('[name=description]').fill('retained unsaved fixture draft');
 await page.locator('[data-action=density]').click();assert.equal(await page.locator('[name=description]').inputValue(),'retained unsaved fixture draft');
 await page.locator('[data-action=density]').click();await page.locator('.board-detail [data-action=close]').click();
 await page.screenshot({path:path.join(output,'compact.png')});await page.reload();await page.locator('.task-card').first().waitFor();assert.equal(await page.locator('#task-board').getAttribute('data-density'),'compact');
 await page.locator('[data-task=stress-0000] [data-action=inspect]').click();assert.equal(await page.locator('[name=description]').inputValue(),'retained unsaved fixture draft');await page.locator('[data-action=reload]').click();await page.locator('.board-detail [data-action=close]').click();
 checks.push('compact: uniform 94px; visible task anchors; blocked hint; retained draft and preference after reload');
 // Actual drag to an already crowded destination, then the keyboard-friendly selector.
 const source=page.locator('[data-task=stress-0004]');await source.dragTo(page.locator('.board-column[data-status=doing] .board-column-heading'));
 await page.waitForFunction(()=>document.querySelector('.board-column[data-status=doing] [data-task=stress-0004]'));
 assert.equal(server.authority.snapshot().model.tasks.find(t=>t.id==='stress-0004').status,'doing');
 await page.locator('[data-task=stress-0004] [data-action=inspect]').click();await page.locator('[name=status]').selectOption('review');await page.locator('.board-save').click();await page.waitForFunction(()=>document.querySelector('.board-column[data-status=review] [data-task=stress-0004]'));
 assert.deepEqual(server.authority.snapshot().model.entities,server.authority.snapshot(1).model.entities);assert.deepEqual(server.authority.snapshot().model.relations,f.model.relations);checks.push('drag and Status selector persist; graph objects unchanged');
 for(const width of [1024,390]){
  await page.setViewportSize({width,height:844});await page.locator('.board-assignee').selectOption('alice');let g=await geometry();assert.equal(g.bodyWidth,width);
  const clear=await page.locator('[data-action=clear]').boundingBox();assert.ok(clear.x+clear.width<=width,JSON.stringify(clear));
  await page.locator('[data-action=clear]').click();await page.locator('.board-columns').evaluate(e=>e.scrollLeft=e.scrollWidth);
  const r=await page.locator('.board-column[data-status=done]').boundingBox();assert.ok(r.x>=0&&r.x+r.width<=width+1);
  await page.screenshot({path:path.join(output,`width-${width}.png`)});
 }
 checks.push('1024/390 width; active filters fit; horizontal scroll reaches Done');
 await page.setViewportSize({width:1440,height:900});
 await page.locator('#surface-canvas').click();await page.locator('[data-otoha-node=input]').waitFor();
 await page.locator('[data-otoha-node=input] [data-otoha-action=select]').click();await page.locator('#graph-filter-title').click();
 const visibleNodes=()=>page.locator('[data-otoha-node]').evaluateAll(es=>es.filter(e=>e.dataset.otohaFilterHidden==='false').map(e=>e.dataset.otohaNode).sort());
 for(const filter of ['downstream','upstream','neighbors','unverified','blocked','all']){
  await page.locator('#graph-filter-strategy').selectOption(filter);
  const q=server.authority.query({mode:'view',view:'overview',filter,...(['upstream','downstream','neighbors'].includes(filter)?{target:'input'}:{})});
  assert.deepEqual(await visibleNodes(),q.records.filter(r=>r.type==='entity').map(r=>r.value.id).sort());
 }
 await page.locator('#graph-filter-title').click();
 await page.locator('[data-otoha-action=submap][data-otoha-key=parser]').press('Enter');await page.locator('[data-otoha-node=asr]').waitFor();
 await page.locator('[data-otoha-action=submap][data-otoha-key="parser/asr"]').press('Enter');await page.locator('[data-otoha-node=decode]').waitFor();
 await page.locator('#graph-filter-title').click();await page.locator('#graph-filter-strategy').selectOption('blocked');
 const expanded=server.authority.query({mode:'view',view:'overview',expanded:'parser,parser/asr',filter:'blocked'});assert.deepEqual(await visibleNodes(),['asr','decode','parser']);assert.equal(expanded.filter.context,2);assert.deepEqual(await visibleNodes(),expanded.records.filter(r=>r.type==='entity').map(r=>r.value.id).sort());
 await page.locator('#graph-data').click();await page.waitForFunction(()=>document.querySelector('#graph-json').textContent.includes('"strategy": "blocked"'));
 const shown=JSON.parse(await page.locator('#graph-json').textContent());assert.deepEqual(shown.records.filter(r=>r.type==='entity').map(r=>r.value.id).sort(),await visibleNodes());await page.locator('#graph-close').click();
 await page.screenshot({path:path.join(output,'canvas-filter.png')});
 await page.locator('#graph-filter-strategy').selectOption('all');assert.equal((await visibleNodes()).length,7);await page.locator('#graph-filter-title').click();
 checks.push('six Canvas presets match Agent IDs; nested containment context; Graph data agrees; clear restores all');
 await page.locator('#surface-board').click();
 const large=JSON.parse(fs.readFileSync(f.input));large.tasks=makeTasks(2000);const c=server.authority.record().cursor;const t=performance.now();fs.writeFileSync(f.input,JSON.stringify(large));server.authority.refresh();await page.waitForFunction(c=>Number(document.querySelector('#task-board').dataset.cursor)>c,c);assert.equal(await page.locator('.task-card').count(),2000);
 metrics.push({case:'2000 accepted update incl polling',milliseconds:Math.round(performance.now()-t)});
 const q=server.authority.query({mode:'board',assignee:'alice',status:'doing',limit:20,maxBytes:12000});assert.equal(q.records.length,20);assert.equal(q.page.hasMore,true);metrics.push({case:'Agent bounded page',records:q.records.length,bytes:Buffer.byteLength(JSON.stringify(q)),filteredCount:q.columns.reduce((n,c)=>n+c.count,0)});
 const ft=performance.now();await page.locator('.board-search input').fill('Synthetic task 1999');assert.equal(await page.locator('.task-card').count(),1);metrics.push({case:'2000 search action',milliseconds:Math.round(performance.now()-ft)});checks.push('2000 records retained; bounded Agent page; targeted search');
 assert.deepEqual(errors,[]);const result={ok:true,scope:'Local macOS Chrome synthetic fixture; not Windows or multi-machine acceptance',checks,metrics};fs.writeFileSync(path.join(output,'result.json'),JSON.stringify(result,null,2)+'\n');console.log(JSON.stringify(result,null,2));
}finally{await browser?.close();await server?.stop();fs.rmSync(f.root,{recursive:true,force:true});}
