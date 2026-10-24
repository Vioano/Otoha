import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { digest, loadModel } from '../../design/model.mjs';

export function fixture(directory) {
  const root = directory || fs.mkdtempSync(path.join(os.tmpdir(), 'archify-design-test-'));
  fs.mkdirSync(root, { recursive: true });
  fs.writeFileSync(path.join(root, 'implementation.mjs'), 'export const result = 1;\n');
  fs.writeFileSync(path.join(root, 'test-result.txt'), 'Isolated fixture: implementation export is 1.\n');
  const binding = { path: 'implementation.mjs', sha256: digest(fs.readFileSync(path.join(root, 'implementation.mjs'))) };
  const source = { id: 'code', kind: 'source', ...binding };
  const testEvidence = { id: 'check', kind: 'test', path: 'test-result.txt', sha256: digest(fs.readFileSync(path.join(root, 'test-result.txt'))), result: 'passed', command: 'isolated fixture assertion', scope: 'Fixture only; not a product validation', capturedAt: new Date().toISOString(), inputs: [binding] };
  function e(id, parent) { return { id, ...(parent?{parent}:{}), type:'backend', label:id, purpose:`Purpose of ${id}`, inputs:['input'],outputs:['output'],steps:['inspect','transform'],openIssues:['Isolated fixture'],evidence:[],maturity:{design:{value:'proposed'},implementation:{value:'not_started'},verification:{value:'untested'},integration:{value:'not_connected'}} }; }
  const model={schema_version:1,diagram_type:'system',meta:{id:'fixture',title:'System fixture',locale:'zh-CN',rootView:'overview'},entities:[e('input'),e('parser'),e('session'),e('asr','parser'),e('music','parser'),e('decode','asr'),e('emit','asr')],relations:[{id:'input-parser',from:'input',to:'parser',kind:'dataflow',label:'audio'},{id:'parser-session',from:'parser',to:'session',kind:'dataflow',label:'evidence'},{id:'decode-emit',from:'decode',to:'emit',kind:'dataflow',label:'text'}],views:[{id:'overview',title:'System overview',placements:[{entity:'input',pos:[40,180]},{entity:'parser',pos:[370,180]},{entity:'session',pos:[700,180]}],relations:[{relation:'input-parser'},{relation:'parser-session'}]},{id:'parser-inside',scope:'parser',title:'Parser internals',placements:[{entity:'asr',pos:[40,180]},{entity:'music',pos:[370,180]}],relations:[]},{id:'asr-inside',scope:'asr',title:'ASR internals',placements:[{entity:'decode',pos:[40,180]},{entity:'emit',pos:[370,180]}],relations:[{relation:'decode-emit'}]}],evidence:[source,testEvidence]};
  model.entities.find(e=>e.id==='decode').evidence=['code','check'];
  model.entities.find(e=>e.id==='decode').maturity.implementation={value:'implemented',evidence:['code']};
  model.entities.find(e=>e.id==='decode').maturity.verification={value:'passed',evidence:['check']};
  const input=path.join(root,'system.json');fs.writeFileSync(input,JSON.stringify(model));
  return {root,input,model,source,testEvidence,options:{input,repoRoot:root},revision:loadModel(input).revision};
}
