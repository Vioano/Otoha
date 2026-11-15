package otoha

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Execute the embedded production host, with only its automatic polling entry
// replaced by a test-side closure export. The DOM/message fixture does not
// claim to verify browser rendering, focus restoration or a real child adapter.
func TestPageHostViewStateLifecycle(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the embedded host lifecycle regression")
	}
	for _, scenario := range []string{
		"same-scope", "scope-isolation", "wrong-source-and-request", "timeout",
		"unsupported", "optional-view-id", "invalid-view-id", "superseded-revision", "team-switch", "feedback",
	} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, node, "--input-type=module", "-e", pageHostLifecycleJS, scenario)
			cmd.Stdin = strings.NewReader(workPageHTML)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("production host scenario %s: %v\n%s", scenario, err, output)
			}
		})
	}
}

const pageHostLifecycleJS = `
import assert from 'node:assert/strict';
import vm from 'node:vm';
import { readFileSync } from 'node:fs';
import { webcrypto } from 'node:crypto';

const html=readFileSync(0,'utf8');
const script=html.match(/<script>([\s\S]*?)<\/script>/)?.[1];
assert.ok(script,'production host script exists');
const boot='restoreDraft();poll();';
assert.equal(script.split(boot).length,2,'one automatic polling entry');
const exported=script.replace(boot,` + "`" + `globalThis.host={renderArtifact,clearArtifact,
 get frame(){return artifactView?.frame||null},get team(){return team},
 switchTeam(value){clearArtifact();team=value;teamVersion++},
 authenticate(){authenticated=true}};` + "`" + `);
const listeners=new Map(),nodes=new Map(),frames=[],calls=[];
let currentData=null;
const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const plain=value=>JSON.parse(JSON.stringify(value));
function element(tag='div'){
 const value={tagName:tag.toUpperCase(),dataset:{},children:[],hidden:false,value:'',textContent:'',attrs:{},isConnected:false,
 classList:{add(){},remove(){},toggle(){}},listeners:new Map(),
 setAttribute(name,v){this.attrs[name]=String(v)},
 addEventListener(name,fn){this.listeners.set(name,fn)},
 append(...children){for(const child of children){child.isConnected=true;child.parentNode=this;this.children.push(child)}},
 replaceChildren(...children){for(const child of this.children)child.isConnected=false;this.children=[];this.append(...children)},
 focus(){},remove(){this.isConnected=false}};
 if(tag==='iframe'){
  value.messages=[];
  value.contentWindow={postMessage(data,origin){value.messages.push(plain(data));if(value.onPost)value.onPost(plain(data),origin)}};
  frames.push(value);
 }
 return value;
}
const document={documentElement:{dataset:{}},hidden:false,
 getElementById(id){if(!nodes.has(id))nodes.set(id,element());return nodes.get(id)},
 createElement:element,createElementNS:(_ns,tag)=>element(tag),addEventListener(){}};
const store=()=>({values:new Map(),getItem(key){return this.values.get(key)??null},setItem(key,v){this.values.set(key,String(v))},removeItem(key){this.values.delete(key)}});
const window={addEventListener(name,fn){if(!listeners.has(name))listeners.set(name,new Set());listeners.get(name).add(fn)},removeEventListener(name,fn){listeners.get(name)?.delete(fn)}};
const fetch=async(path,options={})=>{
 const method=options.method||'GET';const body=options.body?JSON.parse(options.body):null;calls.push({path,method,body});
 let status=200,result={};
 if(path.startsWith('/api/operations')){status=404;result={error:'not_found'}}
 else if(path==='/api/feedback')result={state:'queued',...body};
 else if(path.startsWith('/api/state'))result=currentData;
 else if(path==='/api/teams')result={teams:[{name:'alpha'}]};
 return {ok:status<400,status,json:async()=>result};
};
const context=vm.createContext({window,document,localStorage:store(),sessionStorage:store(),location:{href:'http://localhost/?team=alpha',hash:''},history:{replaceState(){}},URL,URLSearchParams,AbortController,crypto:webcrypto,fetch,setTimeout,clearTimeout,console,structuredClone});
vm.runInContext(exported,context,{timeout:1000});
const host=context.host;
host.switchTeam('alpha');host.authenticate();
async function emit(frame,input,source=frame.contentWindow){
 await Promise.all([...listeners.get('message')||[]].map(fn=>fn({source,data:input})));
}
function page(number,topic='requirements'){
 const document={format:'html',title:'Synthetic requirements',html:'<p>fixture</p>'};
 if(topic!==null)document.view_state={version:1,topic};
 return {request_id:'revision-'+number,message_id:number,document};
}
async function show(number,topic='requirements'){
 const value=page(number,topic);currentData={team:host.team,page:value,connection:'online',feedback:[],pending:[],cursor:number};
 await host.renderArtifact(value,value.document,currentData);return host.frame;
}
const snapshot={theme:'light',view_id:'chapter-rules',details:[{id:'evidence-section',open:true},{id:'program-version',open:false}],scroll:{x:2,y:6738.75},focus_id:'program-summary'};
function captureReplies(frame,state=snapshot){frame.onPost=message=>{if(message.type==='otoha:view-state:capture')queueMicrotask(()=>emit(frame,{type:'otoha:view-state:snapshot',version:1,request_id:message.request_id,state}))};}
async function ready(frame){await emit(frame,{type:'otoha:view-state:ready',version:1})}
const restore=frame=>frame.messages.filter(message=>message.type==='otoha:view-state:restore');
async function scopeSnapshot(){const first=await show(1);await ready(first);captureReplies(first,{...snapshot,form_values:{private:'must not pass'},text:'must not pass'});const second=await show(2);await emit(second,{type:'otoha:view-state:ready',version:1},{});assert.equal(restore(second).length,0,'wrong frame cannot request restoration');await ready(second);return second}

async function test(){
 switch(process.argv[1]){
 case 'same-scope':{
  const second=await scopeSnapshot();assert.equal(restore(second).length,1);assert.deepEqual(restore(second)[0].state,snapshot);await ready(second);assert.equal(restore(second).length,1,'ready replay must not restore again');break;
 }
 case 'scope-isolation':{
  const second=await scopeSnapshot();captureReplies(second);const otherTopic=await show(3,'another-topic');await ready(otherTopic);assert.equal(restore(otherTopic).length,0,'different topic cannot receive the cached state');
  host.switchTeam('beta');const otherTeam=await show(4,'requirements');await ready(otherTeam);assert.equal(restore(otherTeam).length,0,'different team cannot receive the cached state');break;
 }
 case 'wrong-source-and-request':{
  const first=await show(1);await ready(first);let request;
  first.onPost=message=>{if(message.type==='otoha:view-state:capture')request=message};
  const pending=show(2);await sleep(10);assert.ok(request,'capture requested');
  await emit(first,{type:'otoha:view-state:snapshot',version:1,request_id:request.request_id,state:snapshot},{});
  await emit(first,{type:'otoha:view-state:snapshot',version:1,request_id:'wrong-request',state:snapshot});
  await sleep(10);assert.equal(host.frame,first,'wrong sender/request cannot complete capture');
  await emit(first,{type:'otoha:view-state:snapshot',version:1,request_id:request.request_id,state:snapshot});
  const second=await pending;await ready(second);assert.deepEqual(restore(second)[0].state,snapshot);break;
 }
 case 'timeout':{
  const first=await scopeSnapshot();const started=Date.now();const second=await show(3);const elapsed=Date.now()-started;
  assert.notEqual(second,first,'unresponsive adapter cannot block replacement');assert.ok(elapsed<1500,'capture wait is bounded');await ready(second);assert.equal(restore(second).length,0,'timed-out current state must not restore an older cached snapshot');break;
 }
 case 'unsupported':{
  const first=await show(1,null);const started=Date.now();const second=await show(2,null);
  assert.notEqual(second,first);assert.ok(Date.now()-started<1500);assert.equal(first.messages.filter(m=>m.type==='otoha:view-state:capture').length,0,'unadapted page is not asked for view state');await ready(second);assert.equal(restore(second).length,0);break;
 }
 case 'optional-view-id':{
  const first=await show(1);await ready(first);const singleView={...snapshot};delete singleView.view_id;captureReplies(first,singleView);const second=await show(2);await ready(second);assert.deepEqual(restore(second)[0].state,singleView,'single-view pages can omit view_id');break;
 }
 case 'invalid-view-id':{
  const first=await show(1);await ready(first);captureReplies(first,{...snapshot,view_id:{input:'must not pass'}});const second=await show(2);await ready(second);assert.equal(restore(second).length,0,'arbitrary objects cannot pass as a view ID');break;
 }
 case 'superseded-revision':{
  const first=await show(1);await ready(first);let request;first.onPost=message=>{if(message.type==='otoha:view-state:capture')request=message};
  const second=show(2);const third=show(3);await sleep(10);assert.ok(request);
  await emit(first,{type:'otoha:view-state:snapshot',version:1,request_id:request.request_id,state:snapshot});
  await Promise.all([second,third]);assert.equal(host.frame.dataset.pageMessageId,'3','latest pending revision owns the display');assert.equal(frames.filter(f=>f.isConnected).length,1);break;
 }
 case 'team-switch':{
  const first=await show(1);await ready(first);let request;first.onPost=message=>{if(message.type==='otoha:view-state:capture')request=message};
  const pending=show(2);await sleep(10);assert.ok(request);host.switchTeam('beta');const beta=await show(3);
  await emit(first,{type:'otoha:view-state:snapshot',version:1,request_id:request.request_id,state:snapshot});await pending;
  assert.equal(host.frame,beta,'late old-team capture cannot replace the selected team');await ready(beta);assert.equal(restore(beta).length,0);break;
 }
 case 'feedback':{
  const first=await show(1);await ready(first);await emit(first,{type:'otoha:feedback',request_id:'feedback-one',text:'keep original source'},{});assert.equal(calls.filter(c=>c.path==='/api/feedback').length,0);
  await emit(first,{type:'otoha:feedback',request_id:'feedback-one',text:'keep original source'});
  const submission=calls.find(c=>c.path==='/api/feedback');assert.ok(submission);assert.deepEqual(submission.body,{team:'alpha',page_request_id:'revision-1',page_message_id:1,request_id:'feedback-one',text:'keep original source'});
  const replies=first.messages.filter(m=>m.type==='otoha:feedback-result');assert.equal(replies.at(-1).state,'queued');assert.equal(replies.at(-1).request_id,'feedback-one');break;
 }
 default:throw new Error('unknown scenario');
 }
}
try{await test();host.clearArtifact();console.log('host lifecycle:',process.argv[1],'passed');process.exit(0)}catch(error){console.error(error);process.exit(1)}
`
