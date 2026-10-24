import test from 'node:test';
import assert from 'node:assert/strict';
import {otohaExpansionLayout,otohaExpandedRoute} from '../design/nested-layout.mjs';

const nodes=[{id:'in',x:30,y:280,w:180,h:82},{id:'parser',x:290,y:280,w:180,h:82},{id:'out',x:550,y:280,w:180,h:82},{id:'upper',x:290,y:50,w:180,h:82},{id:'lower',x:290,y:570,w:180,h:82}];
const intersects=(a,b)=>a.x<b.x+b.w&&a.x+a.w>b.x&&a.y<b.y+b.h&&a.y+a.h>b.y;
test('expansion preserves ordering, separates neighbours and keeps inverse camera mapping',()=>{
 for(const sizes of [{},{parser:{w:650,h:450}},{parser:{w:650,h:450},out:{w:640,h:480}},{parser:{w:1000,h:800},lower:{w:650,h:450}}]){
  const layout=otohaExpansionLayout(nodes,sizes,900,750),boxes=Object.values(layout.boxes);
  for(let i=0;i<boxes.length;i++)for(let j=i+1;j<boxes.length;j++)assert.equal(intersects(boxes[i],boxes[j]),false);
  for(const point of [[0,0],[388,321],[300,105],[800,700]]){const recovered=layout.inverse(layout.point(point));assert.ok(Math.abs(recovered[0]-point[0])<1e-7);assert.ok(Math.abs(recovered[1]-point[1])<1e-7);}
  assert.ok(layout.width>=900&&layout.height>=750);
 }
});
test('expanded routes remain orthogonal and attached to the correct parent boundaries',()=>{
 const layout=otohaExpansionLayout(nodes,{parser:{w:650,h:450}},900,750);
 const examples=[{from:'in',to:'parser',points:[[210,321],[290,321]]},{from:'parser',to:'out',points:[[470,314],[550,314]]},{from:'parser',to:'lower',points:[[470,338],[505,338],[505,611],[470,611]]}];
 for(const e of examples){const path=otohaExpandedRoute(e.points,nodes.find(n=>n.id===e.from),nodes.find(n=>n.id===e.to),layout);
  for(let i=1;i<path.length;i++)assert.ok(path[i][0]===path[i-1][0]||path[i][1]===path[i-1][1]);
  for(const [p,id] of [[path[0],e.from],[path.at(-1),e.to]]){const b=layout.boxes[id];assert.ok(p[0]>=b.x&&p[0]<=b.x+b.w&&p[1]>=b.y&&p[1]<=b.y+b.h);assert.ok(p[0]===b.x||p[0]===b.x+b.w||p[1]===b.y||p[1]===b.y+b.h);}
 }
});

test('compact whitespace preserves non-overlap and reversible navigation with multiple expanded parents',()=>{
 const sizes={parser:{w:650,h:450},out:{w:640,h:480}},full=otohaExpansionLayout(nodes,sizes,900,750);
 const compact=otohaExpansionLayout(nodes,sizes,900,750,.4),boxes=Object.values(compact.boxes);
 assert.ok(compact.height<full.height);
 for(let i=0;i<boxes.length;i++)for(let j=i+1;j<boxes.length;j++)assert.equal(intersects(boxes[i],boxes[j]),false);
 for(const point of [[300,105],[388,321],[800,700]]){const q=compact.inverse(compact.point(point));assert.ok(Math.hypot(q[0]-point[0],q[1]-point[1])<1e-7);}
});
