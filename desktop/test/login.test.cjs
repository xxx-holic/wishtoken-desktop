'use strict';
const test = require('node:test'), assert = require('node:assert/strict');
const { loginAction, loginURL } = require('../lib/login.cjs');
test('login uses explicit independent grants and session-bound operations', async () => {
  const calls = [], service = {request: async (...args) => {calls.push(args);return {ok:true};}};
  await loginAction(service,'start','bps');
  await loginAction(service,'status','123'); await loginAction(service,'cancel','123');
  assert.deepEqual(calls.map(c=>c.slice(0,3)),[['/api/login/start','POST',{channel:'bps'}],['/api/login/status?session=123'],['/api/login/cancel?session=123','POST',{}]]);
  await assert.rejects(loginAction(service,'start','work'));
  await assert.rejects(loginAction(service,'status','123&channel=codex'));
});
test('only bound OpenAI login destinations can open externally', () => {
  const base = 'https://auth.openai.com/api/accounts/deviceauth/authorize?client_id=app_fnr0pYvVwwFDocDumLG3H2Bp';
  assert.equal(loginURL({channel:'bps',auth_url:base}),base);
  for(const url of [base.replace('https:','http:'),base.replace('auth.openai.com','auth.openai.com.example.test'),base.replace('/authorize?','/other?'),base+'#secret',base.replace('app_fnr0pYvVwwFDocDumLG3H2Bp','wrong'),'file:///tmp/test','javascript:alert(1)']) assert.throws(()=>loginURL({channel:'bps',auth_url:url}));
  assert.throws(()=>loginURL({channel:'codex',auth_url:base}));
});
