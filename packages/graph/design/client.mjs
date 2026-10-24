import path from 'node:path';
import { GraphAuthority, discoverAuthority } from './authority.mjs';
import { problem } from './model.mjs';
import { parseModel } from './model.mjs';
import { readRequests } from './requests.mjs';

export async function connectAuthority(options){
  const saved=discoverAuthority(options),url=options.url||saved?.url;
  if(!options.offline&&url){
    const u=new URL(url);if(u.protocol!=='http:'||u.hostname!=='127.0.0.1')problem('authority/url','Only a local loopback authority is supported');
    try{
      const r=await fetch(new URL('/api/session',u),{signal:AbortSignal.timeout(2500)});if(!r.ok)throw Error(`HTTP ${r.status}`);const session=await r.json();
      if(session.input!==path.resolve(options.input))problem('authority/identity','This endpoint serves a different model',{},409);
      return {url:u.origin+'/',token:session.token,live:true};
    }catch(e){if(e.code==='authority/identity'||options.url)throw e;}
  }
  if(options.requireLive)problem('authority/offline','Start preview first; this operation needs the live authority');
  const authority=new GraphAuthority({...options,stateDir:options.stateDir||saved?.stateDir});authority.record();return {authority,live:false};
}
export async function requestAuthority(connection,route,{query={},payload}={}){
  if(!connection.live)problem('authority/offline','Mutations and subscriptions require a live authority');
  const url=new URL(route,connection.url);for(const [key,value] of Object.entries(query))if(value!==undefined)url.searchParams.set(key,Array.isArray(value)?value.join(','):String(value));
  const r=await fetch(url,{signal:AbortSignal.timeout(60000),...(payload?{method:'POST',headers:{Origin:new URL(connection.url).origin,'Content-Type':'application/json','X-Archify-Token':connection.token},body:JSON.stringify(payload)}:{})});
  const result=await r.json();if(!r.ok)problem(result.code||'authority/http',result.message||result.error,result.details,r.status);return result;
}
export async function readAuthority(options,command,query={}){
  const c=await connectAuthority(options);let result;
  const route={manifest:'manifest',query:'query',inspect:'state',diff:'diff',history:'history',status:'status',requests:'request-state'}[command];
  if(c.live)result=await requestAuthority(c,'/api/'+route,{query});
  else{
    const a=c.authority;
    if(command==='manifest')result=a.manifest();
    else if(command==='query')result=a.query(query);
    else if(command==='inspect')result=a.snapshot(query.cursor);
    else if(command==='diff')result=a.diff(query.after,query.cursor,query);
    else if(command==='history')result=a.history(query);
    else if(command==='requests')result={requests:readRequests({...a.options,getLoaded:()=>parseModel(a.loadObject(a.record().object).source)}),authority:a.status()};
    else result=a.status();
  }
  return {...result,connection:c.live?'live':'offline-accepted-snapshot',...(c.live?{}:{warning:'Authority is offline: this is the last accepted snapshot, not current working-file contents.'})};
}
export async function watchAuthority(options,after,emit=console.log){
  const c=await connectAuthority({...options,requireLive:true});let cursor=Number(after||0),failures=0,stop=false;
  const controller=new AbortController(),end=()=>{stop=true;controller.abort();};process.once('SIGINT',end);process.once('SIGTERM',end);
  try{
    while(!stop){
      try{
        Object.assign(c,await connectAuthority({...options,requireLive:true}));
        const r=await fetch(new URL('api/events?after='+cursor,c.url),{signal:controller.signal});
        if(!r.ok){const error=await r.json();problem(error.code||'authority/watch',error.message||'Subscription failed',error.details,r.status);}
        let pending='';const decoder=new TextDecoder();
        for await(const bytes of r.body){pending+=decoder.decode(bytes,{stream:true});let pos;
          while((pos=pending.indexOf('\n\n'))>=0){const item=pending.slice(0,pos);pending=pending.slice(pos+2);const line=item.split('\n').find(x=>x.startsWith('data: '));if(!line)continue;const event=JSON.parse(line.slice(6));if(event.cursor>cursor){emit(JSON.stringify(event));cursor=event.cursor;failures=0;}}
        }
        if(!stop)throw Error('Event connection ended');
      }catch(error){if(stop)break;if(error.code==='authority/reset-required'||++failures>5)throw error;await new Promise(r=>setTimeout(r,Math.min(500*2**failures,10000)));}
    }
  }finally{process.removeListener('SIGINT',end);process.removeListener('SIGTERM',end);}
}
