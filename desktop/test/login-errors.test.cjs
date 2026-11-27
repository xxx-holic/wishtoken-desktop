'use strict';
const test = require('node:test'), assert = require('node:assert/strict');
const {EventEmitter} = require('node:events');
const {loginProblem, watchAuthFailures} = require('../lib/login-errors.cjs');
const {externalLink} = require('../lib/links.cjs');
test('device login remediation uses a fixed allowlisted destination', () => {
  const p = loginProblem('deviceauth_disabled');
  assert.equal(p.action, 'chatgptSecurity'); assert.match(p.message, /设备码登录/);
  assert.equal(externalLink(p.action), 'https://chatgpt.com/settings/security');
  assert.equal(loginProblem('unknown-secret'), null);
  assert.equal(loginProblem('__proto__'), null);
});
test('browser captures only known auth failure codes and disposes its listener', async () => {
  const page = new EventEmitter(), watch = watchAuthFailures(page);
  const emit = (origin, code) => page.emit('response', {url:()=>origin+'/api/accounts/workspace/select',status:()=>401,text:async()=>JSON.stringify({error:{code,message:'private-fixture-secret'}})});
  emit('https://untrusted.test','deviceauth_disabled'); await new Promise(r=>setImmediate(r)); assert.equal(watch.problem,null);
  emit('https://auth.openai.com','unknown-secret'); await new Promise(r=>setImmediate(r)); assert.equal(watch.problem,null);
  emit('https://auth.openai.com','deviceauth_disabled'); await new Promise(r=>setImmediate(r));
  assert.equal(watch.problem.code,'deviceauth_disabled'); assert.ok(!JSON.stringify(watch).includes('private-fixture-secret'));
  watch.dispose(); assert.equal(page.listenerCount('response'),0);
});
