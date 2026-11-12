'use strict';
const test = require('node:test'), assert = require('node:assert/strict');
const { normalizeChannel, normalizeHistory } = require('../lib/channel-name.cjs');
const { cleanLaunch } = require('../lib/policy.cjs');
const legacy = String.fromCharCode(107, 105, 108, 111);
test('legacy channel settings remain usable and isolated after branding migration', () => {
  assert.equal(normalizeChannel(legacy), 'fenjue');
  assert.equal(normalizeChannel('codex'), 'codex');
  assert.equal(normalizeChannel('unknown'), 'unknown');
  const options = cleanLaunch({ account_id:'acc-test', channel:legacy, model:'gpt-6-astra', effort:'medium', target:'app' });
  assert.equal(options.channel, 'fenjue'); assert.equal(options.app_mode, 'isolated');
});
test('benchmark migration preserves original answers and item identity', () => {
  const answer = 'Original answer mentions ' + legacy;
  const batch = {id:'saved', channel:legacy, items:[{id:'result', route:legacy, answer_text:answer}]};
  normalizeHistory(batch);
  assert.equal(batch.id, 'saved'); assert.equal(batch.channel, 'fenjue');
  assert.equal(batch.items[0].id, 'result'); assert.equal(batch.items[0].route, 'fenjue');
  assert.equal(batch.items[0].answer_text, answer);
});
