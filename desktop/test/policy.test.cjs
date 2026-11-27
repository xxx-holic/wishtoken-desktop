'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { cleanSettings, cleanLaunch, requireID, safeError } = require('../lib/policy.cjs');
test('renderer cannot change local key, listener, route or fallback', () => {
  assert.deepEqual(cleanSettings({ api_key: 'attacker', listen: '0.0.0.0:1', native_fallback: true, route_policy: 'codex_only', desktop_mode: false, auto_refresh: false, proxy_url: ' http://127.0.0.1:7890 ' }), { auto_refresh: false, proxy_url: 'http://127.0.0.1:7890' });
  assert.throws(() => cleanSettings({ usage_probe: 'false' }));
  assert.deepEqual(cleanSettings({bps_transport:'websocket'}), {bps_transport:'websocket'});
  assert.throws(() => cleanSettings({bps_transport:'auto'}));
});
test('launch cannot select arbitrary homes or prepare-only from renderer', () => {
  const request = { account_id: 'acc-abcdef123', directory: '/tmp/项目', model: 'gpt-6-astra', effort: 'xhigh', home: '/Users/test/.codex', prepare_only: true, resume: true };
  const result = cleanLaunch(request);
  assert.equal(result.resume, true);
  assert.equal(result.channel, 'bps');
  assert.equal(result.speed, 'standard');
  assert.equal(cleanLaunch({ ...request, channel: 'codex', speed: 'fast' }).speed, 'fast');
  assert.throws(() => cleanLaunch({ ...request, speed: 'fast' }));
  assert.throws(() => cleanLaunch({ ...request, channel: 'codex', speed: 'ultrafast' }));
  assert.equal(cleanLaunch({ ...request, channel: 'codex' }).channel, 'codex');
  assert.equal(cleanLaunch({ ...request, channel: 'bps_native', speed:'fast' }).channel, 'bps_native');
  assert.throws(() => cleanLaunch({ ...request, channel: 'automatic' }));
  assert.equal(result.home, undefined);
  assert.equal(result.prepare_only, undefined);
  assert.equal(cleanLaunch({ ...request, target: 'app', directory: '' }).app_mode, 'main');
  assert.equal(cleanLaunch({ ...request, target: 'app', app_mode: 'isolated' }).app_mode, 'isolated');
  assert.throws(() => cleanLaunch({ ...request, target: 'app', app_mode: '../../' }));
  assert.throws(() => cleanLaunch({ ...request, effort: 'max' }));
  assert.throws(() => cleanLaunch({ ...request, directory: '' }));
  assert.throws(() => cleanLaunch({ ...request, compact_limit: NaN }));
});
test('Prism launch requires a loopback bridge, isolated app and ultra', () => {
  const base = { account_id: 'acc-abcdef123', target: 'app', model: 'gpt-6.1-sol', effort: 'ultra', channel: 'prism', speed: 'standard', prism_url: 'http://127.0.0.1:18765' };
  const out = cleanLaunch(base);
  assert.equal(out.prism_url, 'http://127.0.0.1:18765');
  assert.equal(out.app_mode, 'isolated');
  assert.equal(cleanLaunch({ ...base, prism_url: 'http://127.0.0.1:18765/v1' }).prism_url, 'http://127.0.0.1:18765');
  assert.throws(() => cleanLaunch({ ...base, prism_url: 'http://example.test:18765' }));
  assert.throws(() => cleanLaunch({ ...base, prism_url: 'https://127.0.0.1:18765' }));
  assert.throws(() => cleanLaunch({ ...base, app_mode: 'main' }));
  assert.throws(() => cleanLaunch({ ...base, effort: 'utlra' }));
  assert.throws(() => cleanLaunch({ ...base, effort: 'xhigh' }));
  assert.throws(() => cleanLaunch({ ...base, channel: 'bps', effort: 'ultra' }));
});
test('account IDs cannot escape the endpoint path; credential errors are redacted', () => {
  for (const id of ['../../settings', 'acc-abc/refresh', 'acc-abc?x=1', '', null]) assert.throws(() => requireID(id));
  assert.equal(safeError(new Error('invalid eyJabc.xyz.def credential')), 'invalid [凭据已隐藏] credential');
});
