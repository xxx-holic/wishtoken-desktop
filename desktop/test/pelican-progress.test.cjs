'use strict';
const test = require('node:test'), assert = require('node:assert/strict');
const fs = require('node:fs'), path = require('node:path'), vm = require('node:vm');
const context = vm.createContext({ $: () => ({}), setInterval: () => 0 });
vm.runInContext(fs.readFileSync(path.join(__dirname, '../renderer/pelican.js'), 'utf8'), context);
const progress = context.pelicanProgress;

test('a completed wrong candy answer is not labelled success', () => {
  assert.equal(progress({ status:'completed', benchmark:'candy', items:[{status:'completed',verdict:'fail',answer:29}] }), '1 / 1 响应完成 · 本题通过 0 · 未通过 1');
});

test('candy counts distinguish pass, fail, review and request failure', () => {
  assert.equal(progress({ status:'completed', benchmark:'candy', items:[
    {status:'completed',verdict:'pass'}, {status:'completed',verdict:'fail'},
    {status:'completed',verdict:'review'}, {status:'failed'}
  ] }), '3 / 4 响应完成 · 本题通过 1 · 未通过 1 · 待确认 1');
});

test('running candy does not score unfinished responses', () => {
  assert.equal(progress({status:'running',benchmark:'candy',items:[{status:'completed',verdict:'pass'},{status:'running'},{status:'queued'}]}), '1 / 3 已结束 · 生成中 · 本题通过 1 · 未通过 0');
});

test('image generation completion does not imply a quality pass', () => {
  assert.equal(progress({status:'completed',benchmark:'pelican',items:[{status:'completed'}]}), '1 / 1 响应完成');
  assert.equal(progress(undefined), '');
});
