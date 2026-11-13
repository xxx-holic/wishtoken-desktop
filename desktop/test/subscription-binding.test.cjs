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

test('binding manager defaults to protocol mode and clears credential fields', async () => {
  const f = fixture();
  const manager = new SubscriptionBinding({
    home: 'unused',
    service: { config: {}, request: async () => ({}) },
    httpFactory: async () => f.http,
    browserFactory: () => { throw new Error('browser must not open'); },
  });
  const start = await manager.start({ account_id: 'fenjue-account', text: `${person.email}----${person.password}----${person.seed}` });
  while (!manager.batch.done) await new Promise(resolve => setTimeout(resolve, 1));
  const result = manager.snapshot(start.id);
  assert.equal(result.mode, 'protocol');
  assert.equal(result.rows[0].status, 'completed');
  assert.equal(result.rows[0].bound_email, person.email);
  assert.ok(!JSON.stringify(result).includes(person.password));
});
