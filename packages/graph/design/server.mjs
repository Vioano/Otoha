import fs from 'node:fs';
import path from 'node:path';
import http from 'node:http';
import { randomBytes } from 'node:crypto';
import { parseModel, digest, resolveBoundFile, problem } from './model.mjs';
import { mutateRequest, readRequests } from './requests.mjs';
import { changeTask } from './tasks.mjs';
import { GraphAuthority, atomicWrite } from './authority.mjs';

export async function startDesignPreview(options) {
  if(options.team?.authority&&path.resolve(options.input)!==path.resolve(options.team.authority.options.input))problem('authority/identity','Preview input must be the team authority model, not its original import',{},409);
  const authority=options.team?.authority||new GraphAuthority(options);if(!authority.writer)authority.acquire();
  try{authority.refresh({forceEvidence:true});if(!authority.current)problem('authority/unavailable','No valid source or durable snapshot is available',authority.status(),503);}catch(e){authority.close();throw e;}
  const token=randomBytes(32).toString('hex'),openCodexFile=options.openCodexFile||null,subscribers=new Set();
  const requestOptions={...options,getLoaded:()=>parseModel(authority.loadObject(authority.record().object).source)};
  let port,timer,heartbeat,stopping=false;
  function requestState(){if(options.team)return {requests:[],requestError:null};try{return {requests:readRequests(requestOptions),requestError:null};}catch(e){return {requests:[],requestError:{code:e.code,message:e.message}};}}
  function status(){const s=authority.status(),r=requestState();return {...s,revisionKey:digest(JSON.stringify([s.cursor,s.failure,r])),requestError:r.requestError};}
  function state(){authority.refresh({forceEvidence:true});return {...authority.snapshot(),...requestState(),...authority.status(),generation:authority.current.cursor,adapter:'manual-cli',revisionKey:status().revisionKey};}
  function notify(){for(const sub of subscribers){try{for(const event of authority.events(sub.cursor)){if(!sub.res.write(`id: ${event.cursor}\nevent: revision\ndata: ${JSON.stringify(event)}\n\n`)){sub.res.destroy();break;}sub.cursor=event.cursor;}}catch{sub.res.destroy();}}}
  const server=http.createServer(async(req,res)=>{
    const headers={'Content-Type':'application/json; charset=utf-8','Cache-Control':'no-store','X-Content-Type-Options':'nosniff','Referrer-Policy':'no-referrer','Cross-Origin-Resource-Policy':'same-origin','X-Frame-Options':'SAMEORIGIN'};
    const send=(code,body,type)=>{res.writeHead(code,{...headers,...(type?{'Content-Type':type}:{})});res.end(typeof body==='string'?body:JSON.stringify(body));};
    try{
      if(req.headers.host!==`127.0.0.1:${port}`)return send(403,{error:'Invalid host'});
      const url=new URL(req.url,`http://127.0.0.1:${port}`),q=Object.fromEntries(url.searchParams);
      const source=req.headers.origin;if(source&&source!==`http://127.0.0.1:${port}`)return send(403,{error:'Same-origin requests only'});
      if(req.method==='GET'){
        if(url.pathname==='/')return send(200,authority.html(),'text/html; charset=utf-8');
        if(url.pathname==='/api/session')return send(200,{token,adapter:'manual-cli',codexFiles:!!openCodexFile,input:path.resolve(options.input),teamRole:options.team?.config.role||null});
        if(url.pathname==='/api/team'&&options.team)return send(200,options.team.teamState());
        if(url.pathname==='/api/status')return send(200,status());
        if(url.pathname==='/api/state')return send(200,q.cursor?{...authority.snapshot(q.cursor),...requestState(),authority:authority.status()}:state());
        if(url.pathname==='/api/request-state')return send(200,{...requestState(),authority:authority.status()});
        if(url.pathname==='/api/manifest')return send(200,authority.manifest());
        if(url.pathname==='/api/query')return send(200,authority.query(q));
        if(url.pathname==='/api/diff')return send(200,authority.diff(q.after,q.cursor,{limit:q.limit,maxBytes:q.maxBytes,page:q.page}));
        if(url.pathname==='/api/history')return send(200,authority.history(q));
        if(url.pathname==='/api/bundle')return send(200,{...authority.bundle(q.cursor),...requestState(),authority:authority.status(),revisionKey:status().revisionKey});
        if(url.pathname==='/api/events'){
          const after=q.after??req.headers['last-event-id']??0;authority.events(after);
          if(subscribers.size>=32)return send(503,{error:'Too many event subscribers; retry later'});
          res.writeHead(200,{...headers,'Content-Type':'text/event-stream','Connection':'keep-alive'});res.write(': connected\n\n');
          const sub={res,cursor:Number(after)};subscribers.add(sub);res.on('close',()=>subscribers.delete(sub));notify();return;
        }
        if(url.pathname==='/api/view'){
          const b=authority.bundle(q.cursor),view=b.views[q.id];return view?send(200,view,'text/html; charset=utf-8'):send(404,{error:'Unknown view'});
        }
        if(url.pathname==='/api/source'){
          const ev=authority.snapshot(q.cursor).model.evidence.find(e=>e.id===q.id);if(!ev)return send(404,{error:'Unknown bound evidence'});
          const file=resolveBoundFile(options.repoRoot,ev.path),bytes=fs.readFileSync(file);if(bytes.includes(0))return send(415,{error:'Binary evidence: inspect its registered path locally'});
          const start=Math.max(0,(ev.line||1)-1),lines=bytes.toString('utf8').split('\n'),end=Math.min(ev.end_line||start+180,start+180);
          return send(200,{path:ev.path,absolutePath:file,status:digest(bytes)===ev.sha256?'verified':'stale',line:start+1,excerpt:lines.slice(start,end).join('\n').slice(0,24000),truncated:lines.length>end});
        }
        return send(404,{error:'Unknown route'});
      }
      if(req.method!=='POST')return send(405,{error:'Unsupported method'});
      if(source!==`http://127.0.0.1:${port}`||req.headers['x-archify-token']!==token||!String(req.headers['content-type']).startsWith('application/json'))return send(403,{error:'Same-origin session token required'});
      let body='';for await(const chunk of req){body+=chunk;if(Buffer.byteLength(body)>100000)problem('request/size','Request too large',{},413);}
      let data;try{data=JSON.parse(body);}catch{problem('request/json','Malformed JSON');}
      if(!data||typeof data!=='object'||Array.isArray(data))problem('request/json','Expected a JSON object');
      if(url.pathname==='/api/team/sync'&&options.team)return send(200,await options.team.sync());
      if(url.pathname==='/api/team/request'&&options.team){
        if(options.team.config.role!=='member')problem('team/role','Member request endpoint only',{},403);
        if(Object.keys(data).some(k=>!['changes','context','requestId'].includes(k)))problem('team/shape','Unknown request field');
        return send(200,{ok:true,request:options.team.prepare(data.changes,data.context,data.requestId).payload});
      }
      if(url.pathname==='/api/team/grant'&&options.team){if(options.team.config.role!=='leader')problem('team/forbidden','Only the local leader manages permissions',{},403);return send(200,{ok:true,cursor:options.team.grant(data).cursor});}
      if(options.team&&['/api/requests','/api/agent-request'].includes(url.pathname))problem('team/forbidden','Use signed team change requests in collaboration mode',{},403);
      if(url.pathname==='/api/tasks'){
        if(options.team?.config.role==='member')problem('team/forbidden','Members submit signed task.set requests with granted field versions',{},403);
        const result=changeTask(authority,data,(source,reason,extra)=>options.team?options.team.transact(source,authority.snapshot().collaboration,reason,extra):authority.transact(source,reason,extra));
        notify();return send(200,result);
      }
      if(url.pathname==='/api/rollback'){const result=authority.rollback(data);notify();return send(200,result);}
      if(url.pathname==='/api/refresh'){authority.refresh({forceEvidence:true});notify();return send(200,status());}
      if(url.pathname==='/api/requests'){
        if(data.action!=='create')problem('request/action','Browser submits design requests only; Agent receipts use the CLI');
        return send(200,await mutateRequest(requestOptions,data));
      }
      if(url.pathname==='/api/agent-request')return send(200,await mutateRequest(requestOptions,data));
      if(url.pathname==='/api/open-in-codex'){
        if(!openCodexFile)return send(503,{error:'当前 Codex 未授权网页直接打开原生文件标签页；请复制路径后在文件面板打开。'});
        const ev=authority.snapshot().model.evidence.find(e=>e.id===data.evidenceId);if(!ev||Object.keys(data).some(k=>k!=='evidenceId'))return send(400,{error:'Only a registered evidence ID is accepted'});
        const file=resolveBoundFile(options.repoRoot,ev.path);await openCodexFile({path:file,line:ev.line||1});return send(200,{ok:true});
      }
      return send(404,{error:'Unknown route'});
    }catch(error){if(!res.headersSent)send(error.status||500,{ok:false,code:error.code||'system/internal',message:error.message,details:error.details});else res.destroy();}
  });
  try{
    await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(options.port||0,'127.0.0.1',resolve);});port=server.address().port;
    const url=`http://127.0.0.1:${port}/`;
    const discovery=JSON.stringify({url,input:path.resolve(options.input),stateDir:path.dirname(authority.directory),pid:process.pid,token});
    for(const dir of new Set([authority.directory,authority.coordinationDirectory]))atomicWrite(path.join(dir,'session.json'),discovery);
    timer=setInterval(()=>{authority.refresh({forceEvidence:true});notify();},options.pollMs||1000);
    heartbeat=setInterval(()=>{for(const {res} of subscribers)res.write(': heartbeat\n\n');},10000);
    return {url,state,authority,stop:async()=>{if(stopping)return;stopping=true;clearInterval(timer);clearInterval(heartbeat);for(const {res} of subscribers)res.end();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));authority.close();}};
  }catch(e){clearInterval(timer);clearInterval(heartbeat);server.close();authority.close();throw e;}
}
