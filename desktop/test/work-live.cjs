'use strict';
// Opt-in integration probe against one explicitly selected, authorized browser
// tab. It reads identity metadata, never exports browser credentials. The local
// fixture has no valid OAuth token and is used ONLY on the Work route.
const fs=require('node:fs'),path=require('node:path'),net=require('node:net'),{randomUUID}=require('node:crypto');
const { Work,pageIdentity }=require('../lib/work.cjs');
const { BridgeService,atomicJSON }=require('../lib/service.cjs');
const { CANDY_PROMPT,PELICAN_PROMPT,candyVerdict }=require('../lib/benchmarks.cjs');
const { extractHTML }=require('../lib/pelican.cjs');
const { MODEL }=require('../lib/work-protocol.cjs');
const root=path.resolve(__dirname,'../..');
(async()=>{
 const target=process.env.WISHTOKEN_WORK_TEST_TARGET;
 if(!target)throw new Error('Set WISHTOKEN_WORK_TEST_TARGET to the authorized dedicated tab ID');
 const home=path.join(root,'build/validation/work-live-'+Date.now());fs.mkdirSync(home,{recursive:true,mode:0o700});
 const worker=new Work(home);let service;
 try{
  const binding={endpoint:process.env.WISHTOKEN_WORK_TEST_CDP || 'http://127.0.0.1:9222',target_id:target};
  const page=await worker.pageFor(binding),identity=await page.evaluate(pageIdentity);
  await worker.listen();
  const socket=net.createServer();await new Promise(r=>socket.listen(0,'127.0.0.1',r));const port=socket.address().port;await new Promise(r=>socket.close(r));
  atomicJSON(path.join(home,'config.json'),{listen:`127.0.0.1:${port}`,auto_refresh:false,usage_probe:false});
  service=new BridgeService({home,binary:path.join(root,'desktop/backend/win-x64/gptbridge.exe'),work:worker});await service.start();
  const imported=await service.request('/api/accounts/import','POST',{email:identity.email,account_id:identity.workspace,access_token:'synthetic-work-test-identity-only',name:'Work integration fixture'});
  const id=imported.ids[0];await worker.bind({...binding,account_id:id},{email:identity.email});
  if(process.env.WISHTOKEN_WORK_TEST_CASE==='pelican'){
   const result=await service.request('/api/pelican/generate','POST',{account_id:id,channel:'work',model:MODEL,effort:'medium',benchmark:'pelican',prompt:PELICAN_PROMPT},330000);
   const html=extractHTML(result.text);atomicJSON(path.join(home,'pelican-result.json'),result);fs.writeFileSync(path.join(home,'pelican.html'),html,{encoding:'utf8',mode:0o600});
   console.log(JSON.stringify({stage:'pelican',complete_html:true,bytes:Buffer.byteLength(html),route:result.route,model:result.response_model,evidence:home}));return;
  }
  const candy=await service.request('/api/pelican/generate','POST',{account_id:id,channel:'work',model:MODEL,effort:'medium',benchmark:'candy',prompt:CANDY_PROMPT},330000);
  atomicJSON(path.join(home,'candy-result.json'),{...candy,...candyVerdict(candy.text)});
  console.log(JSON.stringify({stage:'candy',...candyVerdict(candy.text),route:candy.route,model:candy.response_model,duration_ms:candy.duration_ms}));
  const marker='fixture-'+randomUUID(),fixture={marker,values:[23,41,52]};
  atomicJSON(path.join(home,'fixture.json'),fixture);
  const tools=[{type:'function',name:'read_fixture',description:'Read the local fixture JSON.',parameters:{type:'object',properties:{},additionalProperties:false}},{type:'function',name:'write_result',description:'Write the local result JSON.',parameters:{type:'object',properties:{marker:{type:'string'},total:{type:'integer'}},required:['marker','total'],additionalProperties:false}}];
  const history=[{role:'user',content:'Read the fixture using read_fixture. Sum its values. Use write_result to save exactly its marker and computed total. After the client confirms the write, report the marker and total. Do not guess the contents.'}];
  const records=[];let saved,final;
  for(let turn=0;turn<5;turn++){
   const request={model:MODEL,reasoning:{effort:'medium'},input:history,tools,stream:false};
   // Pin the selected local account through the normal management selection.
   await service.request('/api/codex/select','POST',{account_id:id});
   const result=await service.request('/v1/responses','POST',request,330000);records.push(result);
   const call=result.output.find(x=>x.type==='function_call');
   if(!call){final=result.output.flatMap(x=>x.content || []).map(x=>x.text || '').join('');break;}
   history.push(call);let output;
   if(call.name==='read_fixture')output=JSON.stringify(JSON.parse(fs.readFileSync(path.join(home,'fixture.json'),'utf8')));
   else if(call.name==='write_result'){const value=JSON.parse(call.arguments);if(value.marker!==marker || value.total!==116)throw new Error('Wrong client write result');saved=value;atomicJSON(path.join(home,'result.json'),value);output=JSON.stringify({written:true,marker,total:116});}
   else throw new Error('Undeclared function');
   history.push({type:'function_call_output',call_id:call.call_id,output});
   console.log(JSON.stringify({stage:'tool',turn:turn+1,name:call.name}));
  }
  const passed=!!saved && final?.includes(marker) && final.includes('116');
  atomicJSON(path.join(home,'workflow.json'),{passed,records,final});if(!passed)throw new Error('Client tool workflow incomplete');
  console.log(JSON.stringify({stage:'complete',workflow:true,evidence:home}));
 }finally{await service?.stop();await worker.close();}
})().catch(e=>{console.error(e.message);process.exitCode=1;});
