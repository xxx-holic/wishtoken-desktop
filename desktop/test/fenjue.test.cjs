'use strict';
const test = require('node:test'), assert = require('node:assert/strict');
const {loginAction,loginURL} = require('../lib/login.cjs');
const {cleanLaunch,cleanChannel} = require('../lib/policy.cjs');
const {candyVerdict} = require('../lib/benchmarks.cjs');
test('Fenjue device authorization stays separate and validates its exact destination', async () => {
  const calls=[], service={request:async (...args)=>calls.push(args)};
  await loginAction(service,'start','fenjue');
  await loginAction(service,'status','fenjue-acc-test');
  await loginAction(service,'cancel','fenjue-acc-test');
  assert.deepEqual(calls.map(c=>c[0]),['/api/fenjue/login/start','/api/fenjue/login/status?session=fenjue-acc-test','/api/fenjue/login/cancel?session=fenjue-acc-test']);
  const sample={channel:'fenjue',user_code:'DEMO-1234',auth_url:Buffer.from("aHR0cHM6Ly9hcHAua2lsby5haS9kZXZpY2UtYXV0aD9jb2RlPURFTU8tMTIzNA==", "base64").toString("utf8")};
  assert.equal(loginURL(sample),sample.auth_url);
  for(const auth_url of [sample.auth_url.replace(Buffer.from("YXBwLmtpbG8uYWk=", "base64").toString("utf8"),Buffer.from("YXBwLmtpbG8uYWkuZXhhbXBsZS50ZXN0", "base64").toString("utf8")),sample.auth_url.replace('https:','http:'),sample.auth_url+'#secret',sample.auth_url.replace('1234','4321')]) assert.throws(()=>loginURL({...sample,auth_url}));
  await assert.rejects(loginAction(service,'status','fenjue-acc-test&key=leak'));
});
test('Fenjue launch uses standard speed and an isolated profile', () => {
  assert.equal(cleanChannel('fenjue'),'fenjue');
  const sample={account_id:'acc-test',channel:'fenjue',model:'gpt-6-astra',effort:'medium',target:'app'};
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
