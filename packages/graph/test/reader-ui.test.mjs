import fs from 'node:fs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { parse } from 'parse5';
import { otohaUIText } from '../design/reader-ui.mjs';
const messages=JSON.parse(fs.readFileSync(new URL('../design/ui.en.json',import.meta.url),'utf8'));
const translate=s=>otohaUIText(s,'en',messages);

test('English catalog covers static reader chrome and explicit dynamic UI literals',()=>{
  const viewer=fs.readFileSync(new URL('../design/viewer.html',import.meta.url),'utf8');
  const missed=new Set();
  function check(value){const result=translate(value);if(/[\u3400-\u9fff]/.test(result)&&result.trim()!=='简体中文')missed.add(value);}
  function walk(node){
    if(['script','style','textarea','pre'].includes(node.tagName))return;
    if(node.nodeName==='#text')check(node.value);
    for(const attr of node.attrs||[])if(['aria-label','title','placeholder'].includes(attr.name))check(attr.value);
    for(const child of node.childNodes||[])walk(child);
  }
  walk(parse(viewer));
  for(const source of [viewer,fs.readFileSync(new URL('../team/viewer.mjs',import.meta.url),'utf8')]){
    for(const match of source.matchAll(/\bt\('((?:\\.|[^'\\])*)'\)/g))check(match[1].replaceAll('\\n','\n').replaceAll("\\'","'"));
  }
  assert.deepEqual([...missed],[]);
});

test('UI localization is reversible and does not require changing graph locale',()=>{
  assert.equal(otohaUIText('输入','zh-CN',messages),'输入');
  assert.equal(translate('输入'),'Inputs');
  assert.equal(translate('<section><h2>代码与验证证据</h2>'),'<section><h2>Source & verification evidence</h2>');
  assert.equal(translate('Save & <review>'),'Save & <review>');
});
