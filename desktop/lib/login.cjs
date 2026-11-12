'use strict';
const clients = { bps: 'app_fnr0pYvVwwFDocDumLG3H2Bp', codex: 'app_EMoamEEZ73f0CkXaXp7hrann' };
function channel(value) {
  if (!Object.hasOwn(clients, value)) throw new Error('请选择 Codex 或 BPS 授权');
  return value;
}
function session(value) {
  if (typeof value !== 'string' || !/^\d{1,24}$/.test(value)) throw new Error('登录会话无效');
  return value;
}
function loginURL(data) {
  const type = channel(data.channel), url = new URL(data.auth_url);
  if (url.origin !== 'https://auth.openai.com' || url.username || url.password || url.hash ||
      url.pathname !== (type === 'bps' ? '/api/accounts/deviceauth/authorize' : '/oauth/authorize') ||
      url.searchParams.get('client_id') !== clients[type]) throw new Error('登录地址无效');
  return url.href;
}
async function loginAction(service, action, input) {
  if (action === 'start') return service.request('/api/login/start', 'POST', {channel: channel(input)}, 65000);
  const id = session(input);
  if (action === 'cancel') return service.request(`/api/login/cancel?session=${id}`, 'POST', {});
  if (action === 'status') return service.request(`/api/login/status?session=${id}`);
  throw new Error('登录操作无效');
}
module.exports = { loginAction, loginURL };
