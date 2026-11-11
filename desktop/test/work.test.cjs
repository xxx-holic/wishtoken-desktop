'use strict';
const test=require('node:test'), assert=require('node:assert/strict');
const fs=require('node:fs'),os=require('node:os'),path=require('node:path');
const { Work,endpoint }=require('../lib/work.cjs');
const { planRequest,parseAnswer,responseFor,responseEvents,MODEL,workModelAllowed,workEffortAllowed }=require('../lib/work-protocol.cjs');
const { CANDY_PROMPT,PELICAN_PROMPT,candyVerdict }=require('../lib/benchmarks.cjs');
const { Pelican }=require('../lib/pelican.cjs');
const { cleanLaunch }=require('../lib/policy.cjs');
test('original benchmark bytes have no answer hint or extra instructions',()=>{
 for(const input of [CANDY_PROMPT,PELICAN_PROMPT])assert.equal(planRequest({model:MODEL,input,reasoning:{effort:'medium'}}).prompt,input);
 assert.ok(!CANDY_PROMPT.includes('21'));
 for(const patch of [{model:'gpt-6-astra'},{reasoning:{effort:'ultra'}},{previous_response_id:'resp_1'},{service_tier:'priority'},{tools:[{type:'custom'}]},{input:[{role:'user',content:[{type:'input_image',image_url:'https://example.test/image'}]}]}])assert.throws(()=>planRequest({model:MODEL,input:'hi',...patch}));
 assert.throws(()=>cleanLaunch({account_id:'acc-test',target:'app',channel:'work',model:'gpt-6-astra',effort:'max'}));
 assert.throws(()=>cleanLaunch({account_id:'acc-test',target:'app',channel:'bps',model:'gpt-6-astra',effort:'max'}));
 assert.equal(cleanLaunch({account_id:'acc-test',target:'app',channel:'work',model:'gpt-6.1-sol-wm',effort:'max'}).effort,'max');
});
test('Work accepts pasted images and rejects loss of image history',()=>{
 const png='data:image/png;base64,'+Buffer.from('synthetic-image').toString('base64');
 const one=planRequest({model:MODEL,input:[{role:'user',content:[{type:'input_text',text:'look'},{type:'input_image',image_url:png}]}]});
 assert.equal(one.images.length,1);assert.match(one.prompt,/\[image #1 attached\]/);assert.ok(!one.prompt.includes(png.slice(0,48)));
 const four=planRequest({model:MODEL,input:[{role:'user',content:[...Array(4)].map(()=>({type:'input_image',image_url:png}))}]});
 assert.equal(four.images.length,4);
 assert.throws(()=>planRequest({model:MODEL,input:[{role:'user',content:[...Array(5)].map(()=>({type:'input_image',image_url:png}))}]}),/4/);
 for(const bad of [
  {type:'input_image',image_url:'https://example.test/a.png'},
  {type:'input_image',file_id:'file-1'},
  {type:'input_image',image_url:'data:image/svg+xml;base64,AAAA'},
 ])assert.throws(()=>planRequest({model:MODEL,input:[{role:'user',content:[bad]}]}));
 assert.throws(()=>planRequest({model:MODEL,input:[{role:'user',content:'hi'},{type:'function_call_output',call_id:'c1',output:[{type:'input_image',image_url:png}]}]}));
});
test('Work exposes seven models and all five thinking tiers',()=>{
 const cases={low:'min',medium:'standard',high:'extended',xhigh:'xhigh',max:'max'};
 for(const [client,upstream] of Object.entries(cases)){
  const plan=planRequest({model:'gpt-6.1-sol-wm',input:'hi',reasoning:{effort:client}});
  assert.equal(plan.clientEffort,client);assert.equal(plan.upstreamEffort,upstream);assert.equal(plan.model,'gpt-6.1-sol-wm');
 }
 assert.ok(workModelAllowed('gpt-5.6-luna-wm'));assert.ok(!workModelAllowed('gpt-6-astra'));
 assert.ok(workEffortAllowed('max'));assert.ok(!workEffortAllowed('ultra'));
});
test('client function requests round trip without dropping prior results or executing code',()=>{
 const tools=[{type:'function',name:'read_file',parameters:{type:'object',properties:{path:{type:'string'}}}}];
 const call=parseAnswer('<client_tool_call>{"name":"read_file","arguments":{"path":"fixture.json"}}</client_tool_call>',tools)[0];
 assert.equal(call.name,'read_file');assert.equal(JSON.parse(call.arguments).path,'fixture.json');
 const plan=planRequest({model:MODEL,tools,input:[{role:'user',content:'Read the file'},call,{type:'function_call_output',call_id:call.call_id,output:'unique fixture result'}]});
 assert.ok(plan.prompt.includes('unique fixture result'));
 assert.equal(parseAnswer('<client_tool_call>{"name":"read_file","arguments":{}}',tools)[0].name,'read_file');
 for(const text of ['<client_tool_call>{"name":"unknown","arguments":{}}</client_tool_call>','<client_tool_call>{"name":"read_file","arguments":[]}</client_tool_call>','before <client_tool_call>{}</client_tool_call>','<client_tool_call>{"name":"read_file","arguments":{}}</client_tool_call> after','<client_tool_call>{"name":"read_file"'])assert.throws(()=>parseAnswer(text,tools));
 const events=responseEvents(responseFor('<client_tool_call>{"name":"read_file","arguments":{}}</client_tool_call>',tools));
 assert.equal(events.at(-1).type,'response.completed');assert.ok(events.some(e=>e.type==='response.function_call_arguments.done'));
});
test('freeform custom tools round trip raw apply_patch input as custom_tool_call',()=>{
 const tools=[{type:'custom',name:'apply_patch',description:'Apply a patch',format:{type:'grammar',syntax:'lark',definition:'start: /.+/'}}];
 const plan=planRequest({model:MODEL,tools,input:'Fix the bug'});
 const entry=plan.catalog[0];
 assert.equal(entry.type,'custom');assert.equal(entry.parameters.required[0],'input');assert.match(plan.prompt,/apply_patch/);
 const call=parseAnswer('<client_tool_call>{"name":"apply_patch","arguments":{"input":"*** Begin Patch\\n*** End Patch"}}</client_tool_call>',plan.catalog)[0];
 assert.equal(call.type,'custom_tool_call');assert.ok(call.id.startsWith('ctc_'));assert.equal(call.input,'*** Begin Patch\n*** End Patch');
 const history=planRequest({model:MODEL,tools,input:[{role:'user',content:'Fix the bug'},call,{type:'custom_tool_call_output',call_id:call.call_id,output:'Success'}]});
 assert.match(history.prompt,/custom_tool_call_output/);assert.match(history.prompt,/Success/);
 const events=responseEvents(responseFor('<client_tool_call>{"name":"apply_patch","arguments":{"input":"x"}}</client_tool_call>',plan.catalog));
 assert.ok(events.some(e=>e.type==='response.custom_tool_call_input.done'&&e.input==='x'));
 assert.ok(!events.some(e=>e.type==='response.function_call_arguments.done'));
 for(const text of ['<client_tool_call>{"name":"apply_patch","arguments":{}}</client_tool_call>','<client_tool_call>{"name":"apply_patch","arguments":{"input":1}}</client_tool_call>','<client_tool_call>{"name":"apply_patch","arguments":{"input":"x","extra":"y"}}</client_tool_call>'])assert.throws(()=>parseAnswer(text,plan.catalog));
 assert.equal(planRequest({model:MODEL,tools:[{type:'function',name:'read_file'}],input:'hi'}).catalog[0].type,'function');
});
test('candy verdict reads the conclusion, not any mention of 21; uncertain answers remain reviewable',()=>{
 assert.equal(candyVerdict('答案是 21 颗。').verdict,'pass');
 assert.equal(candyVerdict('21 不够。最少取出 29 颗。').verdict,'fail');
 assert.equal(candyVerdict('最终答案：\\boxed{21}').verdict,'pass');
 assert.equal(candyVerdict('我不确定，可能 21 也可能 29。').verdict,'review');
 assert.equal(candyVerdict('答案为 21','modified question').verdict,'review');
});
test('candy history preserves wrong answers, does not create an HTML preview, and retries the same question',async t=>{
 const home=fs.mkdtempSync(path.join(os.tmpdir(),'wishtoken-candy-')); const seen=[];
 const p=new Pelican(home,async body=>{seen.push(body);return {text:'21 不够。最少取出 29 颗。',duration_ms:1};});
 t.after(async()=>{await p.close();fs.rmSync(home,{recursive:true,force:true});});
 await p.listen(); const accounts=[{id:'acc-fixture',email:'fixture@example.test'}];
 p.start({account_ids:['acc-fixture'],benchmark:'candy',model:MODEL,effort:'medium',channel:'work',concurrency:1},accounts);await p.running;
 const b=p.snapshot()[0],i=b.items[0];assert.equal(i.status,'completed');assert.equal(i.verdict,'fail');assert.equal(i.preview,null);
 assert.equal(seen[0].prompt,CANDY_PROMPT);assert.equal(seen[0].expected_answer,undefined);assert.match(p.artifact(i.id).text,/29/);
 assert.equal((await fetch(`${p.origin}/${p.token}/${i.id}`)).status,404);
 const recovered=new Pelican(home,async()=>{});assert.equal(recovered.snapshot()[0].items[0].verdict,'fail');
});
test('worker auth, loopback endpoint and completed SSE are enforced',async t=>{
 const home=fs.mkdtempSync(path.join(os.tmpdir(),'wishtoken-work-')); const w=new Work(home);
 t.after(async()=>{await w.close();fs.rmSync(home,{recursive:true,force:true});});await w.listen();
 for(const url of ['https://127.0.0.1:9222','http://example.test:9222','http://user:pass@127.0.0.1:9222','http://127.0.0.1:9222/path'])assert.throws(()=>endpoint(url));
 assert.equal((await fetch(w.url+'/responses',{method:'POST',body:'{}'})).status,401);
 w.execute=async()=>responseFor('fixture result',[]);
 const response=await fetch(w.url+'/responses',{method:'POST',headers:{authorization:'Bearer '+w.key},body:JSON.stringify({request:{model:MODEL,input:'fixture'}})});
 const text=await response.text();assert.match(text,/event: response.completed/);assert.match(text,/fixture result/);
 w.execute=async()=>{throw new Error('fixture refusal');};
 const failed=await fetch(w.url+'/responses',{method:'POST',headers:{authorization:'Bearer '+w.key},body:JSON.stringify({request:{model:MODEL,input:'fixture'}})});
 const failure=await failed.text();assert.match(failure,/event: response.failed/);assert.doesNotMatch(failure,/event: response.completed/);
});
