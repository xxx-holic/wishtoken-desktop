'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { externalLink, businessCode } = require('../lib/links.cjs');
test('external navigation accepts only named HTTPS destinations', () => {
  const expected = {
    website: 'https://wishtoapp.com/', shop: 'https://wishtoken.team/', liandong: 'https://wzyp.cn/shop/6YHGJOPN',
    dispatch: 'https://wishtoken.team/shop/dispatch', pricing: 'https://sub2api.wishtoapp.com/guide/channels?utm_source=wishtoken',
    community: 'https://t.me/wishtoken_group', announcements: 'https://t.me/wishtoken_channel', contact: 'https://t.me/wishtoken_team'
  };
  for (const [name, url] of Object.entries(expected)) assert.equal(externalLink(name), url);
  for (const value of ['file:///tmp/a', 'javascript:alert(1)', 'https://evil.test', '__proto__', 'toString', null, {}]) assert.throws(() => externalLink(value));
});
test('business codes are fixed public values, not arbitrary renderer input', () => {
  assert.equal(businessCode('referral'), 'r5tcjnab');
  assert.equal(businessCode('qq'), '1065568670');
  for (const name of ['arbitrary', '__proto__', null, {}]) assert.throws(() => businessCode(name));
});
