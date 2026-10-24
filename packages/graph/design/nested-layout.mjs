// Expand coordinate bands, preserving authored orthogonal routes and ordering.
// Unexpanded cards keep their size inside the space allocated to their band.
export function otohaExpansionLayout(nodes, sizes, width, height, gapY=1) {
  function axis(start, length, target, gap=1) {
    const bands = nodes.map(n => ({ a:n[start], b:n[start]+n[length], ratio:Math.max(1,(sizes[n.id]?.[target] || n[length])/n[length]) }));
    const stops = [...new Set([0,...bands.flatMap(b=>[b.a,b.b])])].sort((a,b)=>a-b);
    const values=[stops[0]];
    for(let i=1;i<stops.length;i++) {
      const mid=(stops[i-1]+stops[i])/2;
      const active=bands.filter(b=>mid>b.a&&mid<b.b);
      const ratio=active.length?Math.max(...active.map(b=>b.ratio)):gap;
      values.push(values[i-1]+(stops[i]-stops[i-1])*ratio);
    }
    const interpolate=(x,from,to)=>{
      if(x<=from[0])return to[0]+x-from[0];
      for(let i=1;i<from.length;i++)if(x<=from[i])return to[i-1]+(x-from[i-1])/(from[i]-from[i-1])*(to[i]-to[i-1]);
      return to.at(-1)+x-from.at(-1);
    };
    return {map:x=>interpolate(x,stops,values),inverse:x=>interpolate(x,values,stops)};
  }
  const x=axis('x','w','w'),y=axis('y','h','h',gapY);
  const boxes=Object.fromEntries(nodes.map(n=>{
    const size=sizes[n.id]||n, w=size.w||n.w,h=size.h||n.h;
    return [n.id,{x:x.map(n.x+n.w/2)-w/2,y:y.map(n.y+n.h/2)-h/2,w,h}];
  }));
  return {boxes,width:x.map(width),height:y.map(height),point:p=>[x.map(p[0]),y.map(p[1])],inverse:p=>[x.inverse(p[0]),y.inverse(p[1])]};
}

// Keep each route outside the allocated node bands while bridging to cards
// whose visible bounds are smaller than their allocated band.
export function otohaExpandedRoute(points, from, to, layout) {
  const warped=points.map(layout.point);
  function port(node,point,mapped) {
    const b=layout.boxes[node.id];
    const sides=[Math.abs(point[0]-node.x),Math.abs(point[0]-node.x-node.w),Math.abs(point[1]-node.y),Math.abs(point[1]-node.y-node.h)];
    const side=sides.indexOf(Math.min(...sides));
    const clamp=(v,a,z)=>Math.max(a,Math.min(z,v));
    if(side<2){const p=[side===0?b.x:b.x+b.w,clamp(mapped[1],b.y+6,b.y+b.h-6)];return [p,[mapped[0],p[1]],mapped];}
    const p=[clamp(mapped[0],b.x+6,b.x+b.w-6),side===2?b.y:b.y+b.h];return [p,[p[0],mapped[1]],mapped];
  }
  const joined=[...port(from,points[0],warped[0]),...warped.slice(1,-1),...port(to,points.at(-1),warped.at(-1)).reverse()];
  return joined.filter((p,i)=>!i||p[0]!==joined[i-1][0]||p[1]!==joined[i-1][1]);
}
