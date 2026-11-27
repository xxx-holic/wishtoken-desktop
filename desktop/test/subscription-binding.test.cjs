'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { SubscriptionLoginProtocol, SubscriptionBinding, WEB_ORIGIN, WEB_CALLBACK_PATH, PROVIDER_CLIENT_ID, providerURL } = require('../lib/subscription-binding.cjs');

const AUTH = 'https://auth.openai.com';
const authURL = `${AUTH}/api/accounts/authorize?client_id=${encodeURIComponent(PROVIDER_CLIENT_ID)}&redirect_uri=${encodeURIComponent(`${WEB_ORIGIN}${WEB_CALLBACK_PATH}`)}&response_type=code&code_challenge=fixture&code_challenge_method=S256&state=fixture-state`;
const person = { email: 'child@example.test', password: 'fixture-password', seed: 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ' };

function fixture(overrides = {}) {
  const calls = [];
  const json = data => ({ status: 200, text: JSON.stringify(data), headers: {} });
  const http = {
    userAgent: 'Synthetic fixture',
    cookies: async () => [],
    close: async () => {},
    request: async (url, options = {}) => {
      calls.push({ url, ...options });
      const parsed = new URL(url), p = parsed.pathname;
      if (overrides[`${parsed.origin}${p}`]) return overrides[`${parsed.origin}${p}`](options);
      if (overrides[p]) return overrides[p](options);
      if (parsed.origin === WEB_ORIGIN && p === '/api/auth/csrf') return json({ csrfToken: 'fixture-csrf' });
      if (parsed.origin === WEB_ORIGIN && p === '/api/auth/signin/openai') return json({ url: authURL });
      if (parsed.origin === WEB_ORIGIN && p === WEB_CALLBACK_PATH) return { status: 302, location: `${WEB_ORIGIN}/byok`, text: '', headers: {} };
      if (parsed.origin === WEB_ORIGIN && p === '/api/auth/session') return json({ user: { email: person.email }, expires: '2099-01-01T00:00:00.000Z' });
      if (parsed.origin === WEB_ORIGIN && p === '/api/trpc/openAiChatGpt.status') return json([{result:{data:{state:'connected',email:person.email}}}]);
      if (p === '/api/accounts/authorize') return json({ continue_url: '/log-in' });
      if (p === '/backend-api/sentinel/req') return json({ token: 'synthetic-security-challenge', proofofwork: { required: false } });
      if (p === '/api/accounts/authorize/continue') return json({ continue_url: '/log-in/password' });
      if (p === '/api/accounts/password/verify') return json({ continue_url: '/mfa-challenge/fixture-factor', page: { type: 'mfa_challenge' } });
      if (p === '/api/accounts/mfa/issue_challenge') return json({});
      if (p === '/api/accounts/mfa/verify') return json({ continue_url: '/sign-in-with-chatgpt/codex/consent', 'oai-client-auth-session': { workspaces: [{ id: 'personal', title: 'Personal' }] } });
      if (p === '/api/accounts/workspace/select') return json({ continue_url: `${WEB_ORIGIN}${WEB_CALLBACK_PATH}?state=fixture-state&code=synthetic-code` });
      if (['/log-in/password', '/mfa-challenge/fixture-factor', '/sign-in-with-chatgpt/codex/consent'].includes(p)) return { status: 200, text: '<html>fixture</html>', headers: {} };
      throw new Error(`Unexpected request ${url}`);
    },
  };
  return { http, calls };
}

test('subscription protocol starts the web OAuth session and completes through the exact callback', async () => {
  const f = fixture();
  const stages = [];
  const protocol = new SubscriptionLoginProtocol(f.http, new AbortController().signal, (stage) => stages.push(stage), { now: () => 60000 });
  const result = await protocol.run({ ...person }, async choices => choices[0]);
  assert.equal(result.email, person.email);
  assert.ok(result.callback.startsWith(`${WEB_ORIGIN}${WEB_CALLBACK_PATH}`));
  assert.deepEqual(stages, ['provider', 'protocol', 'email', 'password', 'mfa', 'consent', 'session']);
  const signIn = f.calls.find(call => new URL(call.url).pathname === '/api/auth/signin/openai');
  const form = new URLSearchParams(signIn.body);
  assert.equal(form.get('csrfToken'), 'fixture-csrf');
  assert.equal(form.get('callbackUrl'), `${WEB_ORIGIN}/byok`);
  // The protocol fixture observes the password at the intended auth boundary;
  // the public manager snapshot never serializes it.
});

test('subscription protocol rejects callback state and untrusted web origins', async () => {
  assert.equal(providerURL(`${WEB_ORIGIN}/api/auth/session`).startsWith(WEB_ORIGIN), true);
  for (const bad of ['https://example.test/api/auth/session', `${WEB_ORIGIN}/api/auth/session#secret`, `${WEB_ORIGIN}/api/auth/session?x=1`]) {
    if (bad.includes('?x=')) assert.equal(providerURL(bad), bad);
    else assert.throws(() => providerURL(bad));
  }
  const f = fixture({ '/api/accounts/workspace/select': () => ({ status: 200, text: JSON.stringify({ continue_url: `${WEB_ORIGIN}${WEB_CALLBACK_PATH}?state=wrong&code=c` }), headers: {} }) });
  await assert.rejects(new SubscriptionLoginProtocol(f.http, new AbortController().signal, () => {}, { now: () => 60000 }).run({ ...person }, async choices => choices[0]), error => error.loginCode === 'identity_mismatch');
  assert.equal(f.calls.filter(call => new URL(call.url).pathname === WEB_CALLBACK_PATH).length, 0);
});

test('ordinary consent submits its form session without workspace or permission selection', async () => {
  const f = fixture({
    '/api/accounts/mfa/verify': () => ({status:200,text:JSON.stringify({continue_url:'/sign-in-with-chatgpt/consent'})}),
    '/sign-in-with-chatgpt/consent': () => ({status:200,text:'<input type="hidden" name="auth_session_id" value="authsess_fixture"/>'}),
    '/api/accounts/consent/grant': options => {
      assert.deepEqual(JSON.parse(options.body), {auth_session_id:'authsess_fixture'});
      return {status:200,text:JSON.stringify({continue_url:`${WEB_ORIGIN}${WEB_CALLBACK_PATH}?state=fixture-state&code=synthetic-code`})};
    },
  });
  const result = await new SubscriptionLoginProtocol(f.http, new AbortController().signal, () => {}, {now:()=>60000})
    .run({...person}, () => {throw Error('unexpected workspace selection');});
  assert.equal(result.email, person.email);
  assert.equal(f.calls.filter(call => new URL(call.url).pathname === '/api/accounts/consent/grant').length, 1);
  assert.ok(!f.calls.some(call => new URL(call.url).pathname === '/api/accounts/consent/availability'));
  const proof = f.calls.filter(call => new URL(call.url).pathname === '/backend-api/sentinel/req').at(-1);
  assert.equal(JSON.parse(proof.body).flow, 'authorize_continue');
});

test('consent rejects missing or ambiguous form sessions before granting', async () => {
  const {LoginProtocol}=require('../lib/login-protocol.cjs');
  for (const text of ['<script>"authsess_unrelated"</script>',
    '<input name="auth_session_id" value="authsess_one"><input name="auth_session_id" value="authsess_two">']) {
    const f=fixture();
    await assert.rejects(new LoginProtocol(f.http,new AbortController().signal,()=>{}).grantConsent({url:AUTH+'/sign-in-with-chatgpt/consent',text}),
      error=>error.loginCode==='consent_context_missing');
    assert.equal(f.calls.length,0);
  }
});

test('subscription performs a second scoped grant and verifies the resulting subscription', async () => {
  let starts=0,checks=0,links=0,grants=0;
  const json=data=>({status:200,text:JSON.stringify(data)});
  const f=fixture({
    '/api/accounts/authorize':()=>json({continue_url:++starts===1?'/log-in':'/choose-an-account'}),
    '/choose-an-account':()=>({status:200,text:'<button name="session_id" value="us_other">other@example.test</button><button name="session_id" value="us_fixture"><span>child@example.test</span></button>'}),
    '/api/accounts/session/select':options=>{
      assert.deepEqual(JSON.parse(options.body),{session_id:'us_fixture'});
      return json({continue_url:'/sign-in-with-chatgpt/consent','oai-client-auth-session':{oauth_resource_consent:{selection:{context:'fixture-context'}}}});
    },
    '/sign-in-with-chatgpt/consent':()=>({status:200,text:'<input value="authsess_resource" name="auth_session_id">'}),
    '/api/accounts/consent/availability':()=>json({context:'fixture-context',availability_receipt:'fixture-receipt'}),
    '/api/accounts/consent/grant':options=>{
      grants++;
      assert.deepEqual(JSON.parse(options.body),{auth_session_id:'authsess_resource',permission_selection:{context:'fixture-context',selected_permissions:['identity','resource_invoke','chatgpt_allowance','offline_access'],availability_receipt:'fixture-receipt'}});
      return json({continue_url:`${WEB_ORIGIN}${WEB_CALLBACK_PATH}?state=fixture-state&code=resource-code`});
    },
    '/api/trpc/openAiChatGpt.status':()=>json([{result:{data:++checks===1?{state:'disconnected'}:{state:'connected',email:person.email}}}]),
    '/api/trpc/user.linkAuthProvider':options=>{links++;assert.deepEqual(JSON.parse(options.body),{'0':{provider:'openai'}});return json([{result:{data:{success:true}}}]);},
  });
  await new SubscriptionLoginProtocol(f.http,new AbortController().signal,()=>{},{now:()=>60000}).run({...person},choices=>choices[0]);
  assert.deepEqual([starts,checks,links,grants],[2,2,1,1]);
  const signins=f.calls.filter(call=>new URL(call.url).pathname==='/api/auth/signin/openai');
  assert.equal(new URL(signins[1].url).searchParams.get('scope'),'openid profile email offline_access resource.invoke chatgpt.tokens.use.obo');
  assert.equal(new URL(signins[1].url).searchParams.get('force_reconsent'),'true');
});

test('callback challenge and subscription cooldown are distinct and never retried', async () => {
  const f=fixture({[WEB_CALLBACK_PATH]:()=>({status:302,text:'',location:'/users/sign_in?error=TURNSTILE_REQUIRED'})});
  await assert.rejects(new SubscriptionLoginProtocol(f.http,new AbortController().signal,()=>{},{now:()=>60000}).run({...person},c=>c[0]),e=>e.loginCode==='browser_required');
  assert.equal(f.calls.filter(c=>new URL(c.url).pathname===WEB_CALLBACK_PATH).length,1);
  let authCalls=0;
  const manager=new SubscriptionBinding({service:{request:async()=>({state:'error',reason:'subscription_cooldown',email:person.email})},httpFactory:()=>{authCalls++;}});
  await manager.start({account_id:'selected',text:[person.email,person.password,person.seed].join('----')});await manager.batch.task;
  assert.equal(authCalls,0);
  assert.match(manager.snapshot().rows[0].message,/24 小时/);
  assert.equal(manager.snapshot().rows[0].verified,undefined);
});

test('binding manager defaults to protocol mode and clears credential fields', async () => {
  const f = fixture();
  let checks = 0;
  const manager = new SubscriptionBinding({
    home: 'unused',
    service: { config: {}, request: async () => ++checks === 1 ? {state:'disconnected'} : {state:'connected',email:person.email} },
    httpFactory: async () => f.http,
    browserFactory: () => { throw new Error('browser must not open'); },
  });
  const start = await manager.start({ account_id: 'fenjue-account', text: `${person.email}----${person.password}----${person.seed}` });
  while (!manager.batch.done) await new Promise(resolve => setTimeout(resolve, 1));
  const result = manager.snapshot(start.id);
  assert.equal(result.mode, 'protocol');
  assert.equal(result.rows[0].status, 'completed');
  assert.equal(result.rows[0].bound_email, person.email);
  assert.equal(result.rows[0].verified, true);
  assert.equal(checks, 2);
  assert.ok(!JSON.stringify(result).includes(person.password));
});

test('already-bound identity is verified without another login or browser', async () => {
  const manager = new SubscriptionBinding({service:{request:async()=>({state:'connected',email:person.email})},
    httpFactory:()=>{throw Error('must not authenticate again');},browserFactory:()=>{throw Error('must not open');}});
  const start=await manager.start({account_id:'selected',text:[person.email,person.password,person.seed].join('----')});
  await manager.batch.task;
  assert.equal(manager.snapshot(start.id).rows[0].status,'completed');
  assert.equal(manager.snapshot(start.id).rows[0].verified,true);
});

test('a different existing binding is preserved without login or mutation', async () => {
  let auth=0;
  const manager=new SubscriptionBinding({service:{request:async()=>({state:'connected',email:'other@example.test'})},httpFactory:()=>{auth++;}});
  await manager.start({account_id:'selected',text:[person.email,person.password,person.seed].join('----')});await manager.batch.task;
  assert.equal(manager.snapshot().rows[0].status,'failed');assert.equal(auth,0);
});

test('web login alone cannot mark the selected subscription connected', async () => {
  for(const state of [{state:'disconnected'},{state:'connected',email:'different@example.test'}]){
    const f=fixture();let checks=0,selectedProxy;
    const manager=new SubscriptionBinding({service:{config:{proxy_url:'http://global.test:80'},accountProxy:async id=>{assert.equal(id,'selected');return 'socks5h://private-proxy.test:1080';},request:async()=>++checks===1?{state:'disconnected'}:state},
      httpFactory:async options=>{selectedProxy=options.proxy;return f.http;}});
    await manager.start({account_id:'selected',text:[person.email,person.password,person.seed].join('----')});await manager.batch.task;
    const row=manager.snapshot().rows[0];assert.equal(row.status,'failed');assert.equal(row.bound_email,undefined);assert.equal(row.verified,undefined);
    assert.equal(selectedProxy,'socks5h://private-proxy.test:1080');assert.equal(checks,2);
  }
});

test('an unavailable preflight stops before credentials are submitted', async () => {
  let opened = 0;
  const manager = new SubscriptionBinding({
    service: {request: async route => { assert.equal(route, '/api/fenjue/subscription?account_id=selected'); return {state: 'error'}; }},
    httpFactory: () => { opened++; }, browserFactory: () => { opened++; },
  });
  await manager.start({account_id: 'selected', text: [person.email, person.password, person.seed].join('----')});
  await manager.batch.task;
  assert.equal(manager.snapshot().rows[0].status, 'failed');
  assert.equal(opened, 0);
});

test('cancellation racing a final status response cannot report successful binding', async () => {
  const f = fixture();
  let resolveStatus, entered;
  const verifying = new Promise(resolve => { entered = resolve; });
  let checks = 0, closed = 0;
  f.http.close = async () => { closed++; };
  const manager = new SubscriptionBinding({
    service: {request: async () => {
      if (++checks === 1) return {state: 'disconnected'};
      entered();
      return new Promise(resolve => { resolveStatus = resolve; });
    }}, httpFactory: async () => f.http,
  });
  const started = await manager.start({account_id: 'selected', text: [person.email, person.password, person.seed].join('----')});
  await verifying;
  const cancelled = manager.cancel(started.id);
  resolveStatus({state: 'connected', email: person.email});
  const result = await cancelled;
  assert.equal(result.rows[0].status, 'cancelled');
  assert.equal(result.rows[0].verified, undefined);
  assert.equal(result.rows[0].bound_email, undefined);
  assert.equal(closed, 1);
});

test('an explicit run proxy is used and final verification stays on the selected account', async () => {
  const f = fixture();
  let checks = 0, usedProxy;
  const manager = new SubscriptionBinding({
    service: {
      accountProxy: async () => { throw Error('explicit proxy must win'); },
      request: async route => {
        assert.equal(route, '/api/fenjue/subscription?account_id=selected');
        return ++checks === 1 ? {state: 'disconnected'} : {state: 'connected', email: person.email};
      },
    },
    httpFactory: async options => { usedProxy = options.proxy; return f.http; },
  });
  await manager.start({account_id: 'selected', proxy_url: 'http://run.test:8080', text: [person.email, person.password, person.seed].join('----')});
  await manager.batch.task;
  assert.equal(manager.snapshot().rows[0].verified, true);
  assert.equal(usedProxy, 'http://run.test:8080');
  assert.equal(checks, 2);
});
