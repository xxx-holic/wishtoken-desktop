'use strict';
const clients = { bps: 'app_fnr0pYvVwwFDocDumLG3H2Bp', codex: 'app_EMoamEEZ73f0CkXaXp7hrann' };
function channel(value) {
  if (value === 'kilo') return value;
  if (!Object.hasOwn(clients, value)) throw new Error('请选择 Codex 或 BPS 授权');
  return value;
}
function session(value) {
  if (typeof value !== 'string' || !/^(?:\d{1,24}|kilo-acc-[a-zA-Z0-9-]{1,80})$/.test(value)) throw new Error('登录会话无效');
  return value;
}
function loginURL(data) {
  const type = channel(data.channel), url = new URL(data.auth_url);
  if (type === 'kilo') {
    if (url.origin !== 'https://app.kilo.ai' || url.pathname !== '/device-auth' || url.username || url.password || url.hash || !data.user_code || url.searchParams.get('code') !== data.user_code) throw new Error('Kilo 授权地址无效');
    return url.href;
  }
  if (url.origin !== 'https://auth.openai.com' || url.username || url.password || url.hash ||
      url.pathname !== (type === 'bps' ? '/api/accounts/deviceauth/authorize' : '/oauth/authorize') ||
      url.searchParams.get('client_id') !== clients[type]) throw new Error('登录地址无效');
  return url.href;
}
async function loginAction(service, action, input) {
  if (action === 'start' && input === 'kilo') return service.request('/api/kilo/login/start', 'POST', {}, 65000);
  if (action === 'start') return service.request('/api/login/start', 'POST', {channel: channel(input)}, 65000);
  const id = session(input);
  if (id.startsWith('kilo-')) {
    if (action === 'cancel') return service.request(`/api/kilo/login/cancel?session=${id}`, 'POST', {});
    if (action === 'status') return service.request(`/api/kilo/login/status?session=${id}`);
    throw new Error('Kilo 登录操作无效');
  }
  if (action === 'cancel') return service.request(`/api/login/cancel?session=${id}`, 'POST', {});
  if (action === 'status') return service.request(`/api/login/status?session=${id}`);
  throw new Error('登录操作无效');
}
module.exports = { loginAction, loginURL };
