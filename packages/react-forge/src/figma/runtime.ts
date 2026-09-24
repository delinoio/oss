/** This code runs only inside official use_figma. Data is JSON encoded separately,
 * never interpolated into executable fragments. No workflow metadata is stored. */
export const runtime = String.raw`
const result={bindings:{...input.bindings},snapshots:{},createdNodeIds:[],mutatedNodeIds:[],deletedNodeIds:[],completed:0};
const fields=['name','x','y','width','height','rotation','opacity','visible','fills','strokes','strokeWeight','cornerRadius','clipsContent','layoutMode','paddingTop','paddingRight','paddingBottom','paddingLeft','itemSpacing','primaryAxisAlignItems','counterAxisAlignItems','primaryAxisSizingMode','counterAxisSizingMode','layoutSizingHorizontal','layoutSizingVertical','characters','fontName','fontSize','textAutoResize','textAlignHorizontal','lineHeight','letterSpacing','vectorPaths','boundVariables','fillStyleId','textStyleId'];
const copy=v=>v===undefined||typeof v==='symbol'?null:JSON.parse(JSON.stringify(v));
const canonical=v=>JSON.stringify(v,(_k,x)=>x&&typeof x==='object'&&!Array.isArray(x)?Object.fromEntries(Object.keys(x).sort().map(k=>[k,x[k]])):x);
const remote=k=>k?.startsWith('@')?k.slice(1):result.bindings[k];
async function lookup(id,kind){
 if(!id)return null;
 if(kind==='COLLECTION')return await figma.variables.getVariableCollectionByIdAsync(id);
 if(kind==='VARIABLE')return await figma.variables.getVariableByIdAsync(id);
 if(kind==='PAINT_STYLE'||kind==='TEXT_STYLE')return await figma.getStyleByIdAsync(id);
 return await figma.getNodeByIdAsync(id);
}
// SHA-256 over UTF-8: the Figma sandbox has no Node crypto module. The compact
// digest keeps preservation guards below MCP's bounded text-result envelope.
function sha256(text){
 const bytes=unescape(encodeURIComponent(text)),words=[],K=[],H=[];let prime=2;
 const frac=x=>(x-Math.floor(x))*4294967296|0;
 for(let i=0;i<64;prime++){let ok=true;for(let j=2;j*j<=prime;j++)if(prime%j===0){ok=false;break;}if(ok){K[i]=frac(Math.cbrt(prime));if(i<8)H[i]=frac(Math.sqrt(prime));i++;}}
 for(let i=0;i<bytes.length;i++)words[i>>2]=(words[i>>2]||0)|bytes.charCodeAt(i)<<(24-i%4*8);
 words[bytes.length>>2]=(words[bytes.length>>2]||0)|0x80<<(24-bytes.length%4*8);
 const length=((bytes.length+8>>6)+1)*16;words[length-1]=bytes.length*8;
 const rotr=(x,n)=>x>>>n|x<<32-n;
 for(let start=0;start<length;start+=16){const w=[];for(let i=0;i<64;i++){const a=w[i-15],b=w[i-2];w[i]=i<16?words[start+i]|0:((rotr(a,7)^rotr(a,18)^a>>>3)+w[i-16]+(rotr(b,17)^rotr(b,19)^b>>>10)+w[i-7])|0;}
 let [a,b,c,d,e,f,g,h]=H;for(let i=0;i<64;i++){const t1=(h+(rotr(e,6)^rotr(e,11)^rotr(e,25))+(e&f^~e&g)+K[i]+w[i])|0;const t2=((rotr(a,2)^rotr(a,13)^rotr(a,22))+(a&b^a&c^b&c))|0;h=g;g=f;f=e;e=d+t1|0;d=c;c=b;b=a;a=t1+t2|0;}const next=[a,b,c,d,e,f,g,h];for(let i=0;i<8;i++)H[i]=H[i]+next[i]|0;}
 return H.map(n=>(n>>>0).toString(16).padStart(8,'0')).join('');
}
function snapshot(n,kind){
 const props={};
 if(kind==='COLLECTION'){props.name=n.name;props.modes=copy(n.modes);props.variableIds=copy(n.variableIds);}
 else if(kind==='VARIABLE'){props.name=n.name;props.resolvedType=n.resolvedType;props.valuesByMode=copy(n.valuesByMode);props.scopes=copy(n.scopes);props.codeSyntax=copy(n.codeSyntax);}
 else if(kind==='PAINT_STYLE'){props.name=n.name;props.paints=copy(n.paints);}
 else {for(const k of fields){if(k in n){try{props[k]=copy(n[k]);}catch{}}}}
 const parent=n.parent?.id||null,children='children'in n?n.children.map(c=>c.id):[];
 // Auto layout and text reflow derive these values from other guarded fields.
 if(n.parent&&'layoutMode'in n.parent&&n.parent.layoutMode!=='NONE'){delete props.x;delete props.y;}
 if(['HUG','FILL'].includes(props.layoutSizingHorizontal)||props.textAutoResize==='WIDTH_AND_HEIGHT')delete props.width;
 if(['HUG','FILL'].includes(props.layoutSizingVertical)||['HEIGHT','WIDTH_AND_HEIGHT'].includes(props.textAutoResize))delete props.height;
 const stateHash=sha256(canonical({id:n.id,type:kind||n.type,parent,children,props}));
 const compact={name:String(n.name||'').slice(0,80)};
 if(Array.isArray(props.fills)){const image=props.fills.find(p=>p.type==='IMAGE');if(image)compact.imageHash=image.imageHash;}
 const box='absoluteBoundingBox'in n?n.absoluteBoundingBox:null;
 return {id:n.id,type:kind||n.type,parent,children:[],props:compact,stateHash,bounds:'width'in n?box||{x:n.x||0,y:n.y||0,width:n.width,height:n.height}:undefined};
}
const loadedFonts=new Set();
async function fonts(n,p){
 const all=[];
 if(n?.type==='TEXT')for(const s of n.getStyledTextSegments(['fontName']))all.push(s.fontName);
 if(p.fontName)all.push(p.fontName);
 if(!n&&p.characters!==undefined&&!p.fontName)all.push({family:'Inter',style:'Regular'});
 for(const f of all){const k=JSON.stringify(f);if(!loadedFonts.has(k)){loadedFonts.add(k);await figma.loadFontAsync(f);}}
}
if(input.page){const p=await lookup(remote(input.page));if(!p||p.type!=='PAGE')throw Error('invalid_target');await figma.setCurrentPageAsync(p);}
if(input.mode==='reconcile'){
 const nodes=[],outcomes=[];
 const subset=(actual,desired)=>Array.isArray(desired)?Array.isArray(actual)&&actual.length===desired.length&&desired.every((v,i)=>subset(actual[i],v)):desired&&typeof desired==='object'?actual&&Object.entries(desired).every(([k,v])=>subset(actual[k],v)):actual===desired;
 for(const op of input.operations){const e=op.entity,id=remote(e.key);if(!id){outcomes.push('unknown');continue;}const n=await lookup(id,e.kind);
  if(!n){outcomes.push(op.action==='delete'?'applied':'unknown');continue;}
  const state=snapshot(n,e.kind);nodes.push(state);
  if(state.stateHash===input.expected?.[id]?.stateHash){outcomes.push('pending');continue;}
  let matches=op.action!=='delete';
  for(const [k,v]of Object.entries(e.props)){
   if(['component','componentProperties','variants','collection','resolvedType','value','scopes','codeSyntax','bindings','fillStyle','textStyle','image','imageScaleMode'].includes(k)){matches=false;break;}
   if(!(k in n)||!subset(copy(n[k]),v)){matches=false;break;}
  }
  if(e.children.length){const ids=e.children.map(remote);matches=matches&&'children'in n&&canonical(n.children.map(c=>c.id).filter(id=>ids.includes(id)))===canonical(ids);}
  outcomes.push(matches?'applied':'unknown');
 }
 return {nodes,outcomes};
}
if(input.mode==='inspect'){
 const nodes=[];
 if(input.resources){const resources=[...(await figma.variables.getLocalVariableCollectionsAsync()).map(n=>({n,kind:'COLLECTION'})),...(await figma.variables.getLocalVariablesAsync()).map(n=>({n,kind:'VARIABLE'})),...(await figma.getLocalPaintStylesAsync()).map(n=>({n,kind:'PAINT_STYLE'})),...(await figma.getLocalTextStylesAsync()).map(n=>({n,kind:'TEXT_STYLE'}))];const offset=input.offset||0;for(const {n,kind}of resources.slice(offset,offset+24))nodes.push(snapshot(n,kind));return {nodes,nextOffset:offset+nodes.length<resources.length?offset+nodes.length:null};}
 else if(input.targets){for(const t of input.targets){const n=await lookup(t.id,t.kind);if(n)nodes.push(snapshot(n,t.kind));}}
 else if(input.page){
  let index=0;const offset=input.offset||0;const walk=(n,depth)=>{if(depth>48||index>=20000)throw Error('resource_limit');if(index>=offset&&nodes.length<24)nodes.push(snapshot(n));index++;if('children'in n)for(const c of n.children)walk(c,depth+1);};walk(figma.currentPage,0);return {nodes,nextOffset:offset+nodes.length<index?offset+nodes.length:null};
 }else{const offset=input.offset||0;for(const p of figma.root.children.slice(offset,offset+24))nodes.push(snapshot(p));return {nodes,nextOffset:offset+nodes.length<figma.root.children.length?offset+nodes.length:null};}
 return {nodes};
}
if(input.mode==='screenshot'){const n=await lookup(input.nodeId);if(!n)throw Error('invalid_target');await n.screenshot({scale:input.scale||1});return {nodeId:n.id};}
const oldIds=new Set(input.operations.filter(o=>o.action==='delete').map(o=>remote(o.entity.key)));
try{
 // Check the complete batch before the first write; never overwrite a foreign
 // save that changed a selected node since our baseline read.
 for(const [id,expected] of Object.entries(input.expected||{})){
  const n=await lookup(id,expected.type);
  if(!n||snapshot(n,expected.type).stateHash!==expected.stateHash){result.errorCode='conflict';return result;}
 }
 // Font readiness is checked before mutation, including current mixed fonts.
 for(const op of input.operations){const n=await lookup(remote(op.entity.key),op.entity.kind);if(op.action==='delete'&&n&&'children'in n&&n.children.some(c=>!oldIds.has(c.id)))throw Error('unsupported_edit');await fonts(n,op.entity.props);}
 for(const op of input.operations){
  const e=op.entity,p=e.props;let n=await lookup(remote(e.key),e.kind);const existed=!!n;
  if(op.action==='delete'){
   if(!n){result.completed++;continue;}
   if('children'in n&&n.children.some(c=>!oldIds.has(c.id)))throw Error('unsupported_edit');
   n.remove();result.deletedNodeIds.push(n.id);delete result.bindings[e.key];result.completed++;continue;
  }
  if(!n){
   if(op.action!=='create')throw Error('invalid_target');
   switch(e.kind){
    case 'PAGE':n=figma.createPage();break;
    case 'FRAME':n=figma.createFrame();break;
    case 'TEXT':n=figma.createText();break;
    case 'RECTANGLE':n=figma.createRectangle();break;
    case 'ELLIPSE':n=figma.createEllipse();break;
    case 'LINE':n=figma.createLine();break;
    case 'VECTOR':n=figma.createVector();break;
    case 'COMPONENT':n=figma.createComponent();break;
    case 'INSTANCE':{const c=await lookup(remote(p.component));if(c?.type!=='COMPONENT')throw Error('invalid_target');n=c.createInstance();break;}
    case 'COMPONENT_SET':{const cs=[];for(const k of p.variants){const c=await lookup(remote(k));if(c?.type!=='COMPONENT')throw Error('invalid_target');cs.push(c);}const parent=await lookup(remote(e.parent));n=figma.combineAsVariants(cs,parent||figma.currentPage);break;}
    case 'COLLECTION':n=figma.variables.createVariableCollection(p.name||'Variables');break;
    case 'VARIABLE':{const c=await lookup(remote(p.collection),'COLLECTION');if(!c)throw Error('invalid_target');n=figma.variables.createVariable(p.name||'Variable',c,p.resolvedType);break;}
    case 'PAINT_STYLE':n=figma.createPaintStyle();break;
    case 'TEXT_STYLE':n=figma.createTextStyle();break;
    default:throw Error('unsupported_edit');
   }
   result.bindings[e.key]=n.id;result.createdNodeIds.push(n.id);
   if(e.parent&&e.kind!=='COMPONENT_SET'&&e.kind!=='VARIABLE'){
    const parent=await lookup(remote(e.parent));if(!parent||!('appendChild'in parent))throw Error('invalid_target');parent.appendChild(n);
   }
  }else if(n.type!==e.kind&& !['COLLECTION','VARIABLE','PAINT_STYLE','TEXT_STYLE'].includes(e.kind))throw Error('unsupported_edit');
  if(p.layoutMode!==undefined)n.layoutMode=p.layoutMode;
  if((p.width!==undefined||p.height!==undefined)&&'resize'in n)n.resize(p.width??n.width,p.height??n.height);
  if(p.fontName)n.fontName=p.fontName;
  for(const [k,v]of Object.entries(p)){
   if(['width','height','layoutMode','fontName','component','componentProperties','variants','collection','resolvedType','value','scopes','codeSyntax','bindings','fillStyle','textStyle','image','imageScaleMode','layoutSizingHorizontal','layoutSizingVertical'].includes(k))continue;
   if(e.kind==='PAINT_STYLE'&&k==='fills')n.paints=v;else n[k]=v;
  }
  if(e.kind==='VARIABLE'){n.scopes=p.scopes;const c=await lookup(n.variableCollectionId,'COLLECTION');n.setValueForMode(c.defaultModeId,p.value);if(p.codeSyntax)for(const [platform,syntax]of Object.entries(p.codeSyntax))n.setVariableCodeSyntax(platform,syntax);}
  if(p.image&&input.images?.[p.image])n.fills=[{type:'IMAGE',imageHash:input.images[p.image],scaleMode:p.imageScaleMode||'FILL'}];
  if(p.componentProperties)n.setProperties(p.componentProperties);
  if(p.fillStyle)await n.setFillStyleIdAsync(remote(p.fillStyle));
  if(p.textStyle)await n.setTextStyleIdAsync(remote(p.textStyle));
  if(p.bindings)for(const [field,key]of Object.entries(p.bindings)){
   const v=await lookup(remote(key),'VARIABLE');if(!v)throw Error('invalid_target');
   if(field==='fills'){const paint=n.fills[0]||{type:'SOLID',color:{r:0,g:0,b:0}};n.fills=[figma.variables.setBoundVariableForPaint(paint,'color',v),...n.fills.slice(1)];}else n.setBoundVariable(field,v);
  }
  for(const field of ['layoutSizingHorizontal','layoutSizingVertical'])if(p[field]!==undefined)n[field]=p[field];
  if(e.children.length&&'insertChild'in n){
   const ids=e.children.map(remote).filter(Boolean);
   // Reorder only managed slots; foreign siblings retain their relative order.
   const slots=n.children.map((c,i)=>ids.includes(c.id)?i:-1).filter(i=>i>=0);
   for(let i=0;i<ids.length;i++){const child=await lookup(ids[i]);if(child?.parent?.id===n.id)n.insertChild(slots[i]??n.children.length-1,child);}
  }
  if(existed)result.mutatedNodeIds.push(n.id);
  result.completed++;
 }
}catch(error){result.errorCode=['conflict','invalid_target','unsupported_edit','resource_limit'].includes(error?.message)?error.message:'remote';}
// Return actual post-write state even on a property setter failure. A caller can
// reconcile known creations without repeating them; lost responses remain unknown.
const capture=(n,kind)=>{result.snapshots[n.id]=snapshot(n,kind);};
for(const op of input.operations){const id=remote(op.entity.key);const n=await lookup(id,op.entity.kind);if(n){capture(n,op.entity.kind);if(n.parent?.type!=='DOCUMENT'&&n.parent)result.snapshots[n.parent.id]=snapshot(n.parent);}}
result.bindings=Object.fromEntries(input.operations.map(op=>[op.entity.key,result.bindings[op.entity.key]]).filter(([,id])=>id));
return result;
`;
export function script(input: Record<string, unknown>): string {
  return `const input=${JSON.stringify(input)};\n${runtime}`;
}
