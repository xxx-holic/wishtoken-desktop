'use strict';
const test = require('node:test'), assert = require('node:assert/strict');
const { AutoLogin, parseCredentials, totp } = require('../lib/auto-login.cjs');
const { setTimeout: delay } = require('node:timers/promises');
const seed = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ'; // RFC 6238 synthetic test key.
const raw = `child@example.test----synthetic----password----${seed}`;
const clients = {codex:'app_EMoamEEZ73f0CkXaXp7hrann',bps:'app_fnr0pYvVwwFDocDumLG3H2Bp'};
test('credential parser supports multiline, CRLF, empty MFA and passwords containing separators', () => {
  const rows = parseCredentials('\uFEFF'+raw+'\r\n\r\nsecond@example.test----p----');
  assert.equal(rows.length, 2); assert.equal(rows[0].password, 'synthetic----password'); assert.equal(rows[1].seed, '');
  for (const input of ['', 'not-email----p----', 'child@example.test----p', `child@example.test----p----123456`, raw+'\n'+raw, 'x'.repeat(65537)]) {
    assert.throws(() => parseCredentials(input), error => !error.message.includes('synthetic----password') && !error.message.includes(seed));
  }
});
test('TOTP matches RFC 6238 golden vectors, including future counters', () => {
  for (const [seconds, answer] of [[59,'94287082'],[1111111109,'07081804'],[1111111111,'14050471'],[1234567890,'89005924'],[2000000000,'69279037'],[20000000000,'65353130']]) assert.equal(totp(seed, seconds * 1000, 8), answer);
  assert.equal(totp(seed,59000),'287082');
  assert.throws(() => totp('invalid!'));
});
function harness() {
  const calls = [], browserCalls = []; let id = 10;
  const service = { request: async (route, method, body) => {
    calls.push({route,method,body});
    if (route === '/api/login/status') return {active:false};
    if (route === '/api/login/start') return {session:String(++id),channel:body.channel,auth_url:`https://auth.openai.com/${body.channel === 'bps' ? 'api/accounts/deviceauth/authorize' : 'oauth/authorize'}?client_id=${clients[body.channel]}`};
    return {};
  }};
  const browserFactory = async () => {
    const b = {closed:0,page:{goto:async()=>{},isClosed:()=>false,bringToFront:async()=>{}},close:async()=>{b.closed++;}};
    browserCalls.push(b); return b;
  };
  const manager=new AutoLogin({service,home:'unused',browserFactory,interval:1});
  const start=manager.start.bind(manager);manager.start=input=>start({mode:'browser',...input});
  return {manager,calls,browserCalls};
}
test('batch runs sequentially with identity binding, independent channel and secret-free snapshots', async () => {
  const {manager,calls,browserCalls}=harness(); const people=[];
  manager.drive=async (_batch,row,person,_login,_page,update) => { people.push(person);row.account_id='acc-'+row.index;update('completed','saved','completed'); };
  const initial=await manager.start({channel:'bps',text:raw+'\nsecond@example.test----p----'});
  await manager.batch.task;
  const final=manager.snapshot(initial.id);
  assert.equal(final.done,true); assert.deepEqual(final.rows.map(r=>r.status),['completed','completed']);
  assert.equal(browserCalls.length,2);assert.ok(browserCalls.every(b=>b.closed===1));
  const starts=calls.filter(c=>c.route==='/api/login/start');
  assert.deepEqual(starts.map(c=>c.body),[{channel:'bps',expected_email:'child@example.test',fresh:true},{channel:'bps',expected_email:'second@example.test',fresh:true}]);
  assert.ok(people.every(p=>!p.password&&!p.seed));
  const output=JSON.stringify({final,calls});assert.ok(!output.includes(seed));assert.ok(!output.includes('synthetic----password'));
});
test('cancel invalidates active work, clears remaining secrets and cannot target a newer batch', async () => {
  const {manager,browserCalls}=harness(); let entered;
  const ready = new Promise(r=>{entered=r;});
  manager.drive=async (_b,_r,_p,_l,_page,_u,signal)=>{entered();await delay(10000,undefined,{signal});};
  const started=await manager.start({channel:'codex',text:raw+'\nsecond@example.test----p----'});await ready;
  await assert.rejects(manager.start({channel:'bps',text:raw}));
  await assert.rejects(manager.cancel('stale'));
  const result=await manager.cancel(started.id);assert.ok(result.done);assert.ok(result.rows.every(r=>r.status==='cancelled'));assert.equal(browserCalls.length,1);
  assert.throws(()=>manager.choose({id:started.id,index:0,choice:'unknown'}));
});
test('one account failure does not stop the queue or expose browser exception details', async () => {
  const {manager}=harness();
  manager.drive=async (_b,row,_p,_l,_page,update)=>{if(row.index===0)throw Error('fill('+seed+') synthetic----password');row.account_id='acc-safe';update('completed','saved','completed');};
  const start=await manager.start({channel:'codex',text:raw+'\nsecond@example.test----p----'});await manager.batch.task;
  const result=manager.snapshot(start.id);assert.deepEqual(result.rows.map(r=>r.status),['failed','completed']);
  assert.ok(!JSON.stringify(result).includes(seed));assert.ok(!JSON.stringify(result).includes('synthetic----password'));
});
test('skip cancels only the current row and continues the following account', async () => {
  const {manager}=harness();let entered;const ready=new Promise(r=>{entered=r;});
  manager.drive=async(_b,row,_p,_l,_page,update,signal)=>{if(!row.index){entered();await delay(10000,undefined,{signal});}else{row.account_id='acc-safe';update('completed','saved','completed');}};
  const start=await manager.start({channel:'bps',text:raw+'\nsecond@example.test----p----'});await ready;manager.skip(start.id);await manager.batch.task;
  assert.deepEqual(manager.snapshot(start.id).rows.map(r=>r.status),['cancelled','completed']);
});
test('manual login preflight and simultaneous starts cannot reuse another session', async () => {
  const {manager}=harness();manager.drive=async(_b,_r,_p,_l,_page,_u,signal)=>delay(10000,undefined,{signal});
  const results=await Promise.allSettled([manager.start({channel:'codex',text:raw}),manager.start({channel:'codex',text:raw})]);
  assert.equal(results.filter(r=>r.status==='fulfilled').length,1);await manager.close();
  manager.service.request=async()=>({active:true});await assert.rejects(manager.start({channel:'bps',text:raw}),/浏览器授权/);
});
