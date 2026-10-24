import fs from 'node:fs';
import path from 'node:path';
import { buildDesign, deliverDesign } from './deliver.mjs';
import { startDesignPreview } from './server.mjs';
import { problem } from './model.mjs';
import { mutateRequest, readRequests, journalPath } from './requests.mjs';
import { readAuthority, connectAuthority, requestAuthority, watchAuthority } from './client.mjs';
import { discoverAuthority } from './authority.mjs';
import { openLoopbackUrl } from '../bin/open-artifact.mjs';

export async function runDesign(args) {
  const [command, input, ...rest] = args;
  const help = `Otoha Graph · system design
  otoha-graph validate <system.json> [--repo-root path] [--json]
  otoha-graph deliver <system.json> <output.html> [--repo-root path] [--json]
  otoha-graph preview <system.json> [--repo-root path] [--state-dir path] [--port n] [--open]
  otoha-graph manifest <system.json> [--url http://127.0.0.1:PORT]
  otoha-graph query <system.json> --mode overview|local|reach|path|cycles|view|board|full
      [--target id] [--from id --to id] [--view id] [--expanded parser,parser/asr] [--depth n] [--hops n]
      [--assignee id] [--status todo|doing|review|done] [--search text]
      [--direction in|out|both] [--kinds dataflow,call] [--detail summary|full]
      [--cursor n] [--limit n] [--max-bytes n] [--page token] [--offline]
  otoha-graph diff <system.json> --after n [--cursor n] [--page token]
  otoha-graph history <system.json> | status <system.json>
  otoha-graph watch <system.json> [--after n]    # NDJSON, Ctrl-C stops
  otoha-graph task <system.json> --payload task-change.json
  otoha-graph rollback <system.json> --payload rollback.json
  otoha-graph inspect <system.json> [--offline]  # explicit full snapshot
  otoha-graph requests <system.json> [--repo-root path] [--state-dir path]
  otoha-graph submit <system.json> --payload request.json [--state-dir path]
  otoha-graph receive <system.json> --payload receive.json [--repo-root path] [--state-dir path]
  otoha-graph report <system.json> --payload receipt.json [--repo-root path] [--state-dir path]
  otoha-graph rebase <system.json> --payload rebase.json [--state-dir path]

Preview never starts an Agent. requests/receive/report are the manual JSON adapter.
Use the exact model revision and request version returned by inspect/requests.
See references/system-design-contract.md for payloads and evidence contracts.`;
  if (!command || ['help','--help','-h'].includes(command)) { console.log(help); return; }
  try {
    if (!input || input.startsWith('--')) problem('design/usage', help);
    const options = { input: path.resolve(input) }; const positional = []; const query = {};
    for (let i = 0; i < rest.length; i++) {
      const key = rest[i];
      if (key === '--json' || key === '--no-open') continue;
      if (key === '--offline') { options.offline = true; continue; }
      if (['--mode','--target','--from','--to','--view','--expanded','--depth','--hops','--direction','--kinds','--detail','--cursor','--limit','--max-bytes','--page','--after','--url','--assignee','--status','--search','--filter'].includes(key)) {
        const value=rest[++i]; if(!value||value.startsWith('--'))problem('design/usage', `${key} needs a value`);
        if(key==='--url')options.url=value;else query[key==='--max-bytes'?'maxBytes':key.slice(2)]=value;continue;
      }
      if (key === '--open') { options.open = true; continue; }
      if (['--repo-root','--state-dir','--payload','--port'].includes(key)) {
        const value = rest[++i]; if (!value || value.startsWith('--')) problem('design/usage', `${key} needs a value`);
        if (key === '--port') { options.port = Number(value); if (!Number.isInteger(options.port) || options.port < 0 || options.port > 65535) problem('design/port', 'Invalid port'); }
        else options[{ '--repo-root': 'repoRoot', '--state-dir': 'stateDir', '--payload': 'payload' }[key]] = path.resolve(value);
      } else if (key.startsWith('--')) problem('design/usage', `Unknown option ${key}`);
      else positional.push(key);
    }
    if (positional.length > (command === 'deliver' ? 1 : 0)) problem('design/usage', 'Unexpected positional argument');
    if (command === 'validate') console.log(JSON.stringify(buildDesign(options.input, options).receipt, null, 2));
    else if (command === 'deliver') console.log(JSON.stringify(deliverDesign(options.input, positional[0], options).receipt, null, 2));
    else if (['manifest','query','inspect','diff','history','status'].includes(command)) console.log(JSON.stringify(await readAuthority(options, command, query), null, 2));
    else if(command==='watch') await watchAuthority(options,query.after);
    else if(['rollback','task'].includes(command)){
      if(!options.payload)problem('design/usage','--payload is required');
      const connection=await connectAuthority({...options,requireLive:true});
      console.log(JSON.stringify(await requestAuthority(connection,command==='task'?'/api/tasks':'/api/rollback',{payload:JSON.parse(fs.readFileSync(options.payload,'utf8'))}),null,2));
    }
    else if (command === 'requests') console.log(JSON.stringify(discoverAuthority(options) ? await readAuthority(options,'requests') : { adapter: 'manual-cli', journal: journalPath(options.input, options.stateDir), requests: readRequests(options) }, null, 2));
    else if (['submit','receive','report','rebase'].includes(command)) {
      if (!options.payload) problem('design/usage', '--payload is required; no shell command is executed from payloads');
      const data = JSON.parse(fs.readFileSync(options.payload, 'utf8'));
      const expected = { submit: 'create', receive: 'report', report: 'report', rebase: 'rebase' }[command];
      if (data.action && data.action !== expected) problem('design/usage', 'Payload action does not match command');
      if (command === 'receive' && data.stage && data.stage !== 'accepted') problem('design/usage', 'receive records accepted only');
      const payload = { ...data, action: expected, ...(command === 'receive' ? { stage: 'accepted' } : {}) };
      const result = discoverAuthority(options) ? await requestAuthority(await connectAuthority({...options,requireLive:true}),'/api/agent-request',{payload}) : await mutateRequest(options,payload);
      console.log(JSON.stringify(result, null, 2));
    } else if (command === 'preview') {
      const server = await startDesignPreview(options);
      console.log(JSON.stringify({ ok: true, url: server.url, adapter: 'manual-cli', journal: journalPath(options.input, options.stateDir), ...(options.open ? { open: openLoopbackUrl(server.url) } : {}) }));
      let stopping = false;
      const stop = async () => { if (stopping) return; stopping = true; await server.stop(); process.exit(0); };
      process.on('SIGINT', stop); process.on('SIGTERM', stop);
    } else problem('design/usage', `Unknown design command ${command}\n${help}`);
  } catch (error) {
    console.error(JSON.stringify({ ok: false, code: error.code || 'design/error', message: error.message, details: error.details }, null, 2));
    process.exitCode = 1;
  }
}
