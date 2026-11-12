'use strict';
const test = require('node:test'), assert = require('node:assert/strict');
const {loginAction,loginURL} = require('../lib/login.cjs');
const {cleanLaunch,cleanChannel} = require('../lib/policy.cjs');
const {candyVerdict} = require('../lib/benchmarks.cjs');
test('Kilo device authorization stays separate and validates its exact destination', async () => {
  const calls=[], service={request:async (...args)=>calls.push(args)};
  await loginAction(service,'start','kilo');
  await loginAction(service,'status','kilo-acc-test');
  await loginAction(service,'cancel','kilo-acc-test');
  assert.deepEqual(calls.map(c=>c[0]),['/api/kilo/login/start','/api/kilo/login/status?session=kilo-acc-test','/api/kilo/login/cancel?session=kilo-acc-test']);
  const sample={channel:'kilo',user_code:'DEMO-1234',auth_url:'https://app.kilo.ai/device-auth?code=DEMO-1234'};
  assert.equal(loginURL(sample),sample.auth_url);
  for(const auth_url of [sample.auth_url.replace('app.kilo.ai','app.kilo.ai.example.test'),sample.auth_url.replace('https:','http:'),sample.auth_url+'#secret',sample.auth_url.replace('1234','4321')]) assert.throws(()=>loginURL({...sample,auth_url}));
  await assert.rejects(loginAction(service,'status','kilo-acc-test&key=leak'));
});
test('Kilo launch uses standard speed and an isolated profile', () => {
  assert.equal(cleanChannel('kilo'),'kilo');
  const sample={account_id:'acc-test',channel:'kilo',model:'gpt-6-astra',effort:'medium',target:'app'};
  assert.equal(cleanLaunch(sample).app_mode,'isolated');
  assert.throws(()=>cleanLaunch({...sample,app_mode:'main'}));
  assert.throws(()=>cleanLaunch({...sample,speed:'fast'}));
  assert.throws(()=>cleanLaunch({...sample,effort:'max'}));
});
test('candy proof subcounts do not conflict with an explicit total', () => {
  assert.equal(candyVerdict('最少取出 21 个糖果。至少需要摸出 9 个圆形和 12 个五角星形。').verdict,'pass');
  assert.equal(candyVerdict('最少取出 29 个糖果。至少需要摸出 9 个圆形。').verdict,'fail');
  assert.equal(candyVerdict('最少需要取出21个。圆形至少9个，五角星形至少5个。').verdict,'pass');
  assert.equal(candyVerdict('答案是21颗。若不能选择形状，答案是29颗。').verdict,'review');
});
