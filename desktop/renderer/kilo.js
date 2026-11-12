'use strict';
let kiloSession = null, kiloTimer, kiloGeneration = 0;
async function loadKilo(preferred) {
  const select = $('kilo-account'), selected = preferred || select.value || state.selected;
  select.innerHTML = (state.data?.accounts || []).filter(a => a.provider === 'kilo').map(a => `<option value="${esc(a.id)}">${esc(label(a))}</option>`).join('');
  select.value = selected; if (!select.value) select.selectedIndex = 0;
  $('kilo-check').disabled = !select.value;
  $('kilo-status').textContent = select.value ? '点击检查，核对已绑定的 ChatGPT 账号和订阅状态。' : '先授权 Kilo，或导入 type 为 kilo 的 JSON。';
}
$('kilo-setup').onclick = () => action('kilo-setup', async () => { switchPage('settings'); await loadKilo(); $('kilo-settings').scrollIntoView(); });
$('kilo-byok').onclick = () => action('kilo-byok', () => api.kiloOpen());
$('kilo-account').onchange = () => { $('kilo-check').disabled = !$('kilo-account').value; $('kilo-status').textContent = '请检查当前账号的订阅绑定。'; };
$('kilo-check').onclick = () => action('kilo-check', async () => {
  const result = await api.kiloSubscription($('kilo-account').value);
  $('kilo-status').textContent = result.state === 'connected' ? `已绑定 ${result.email || 'ChatGPT 订阅'}${result.usageLimit ? ' · 有历史限额提示，实际可用性以本次请求为准' : ''}` : result.state === 'disconnected' ? '尚未绑定 ChatGPT；请打开订阅设置完成连接。' : '订阅连接异常；请在 Kilo 设置重新连接。';
});
$('kilo-login').onclick = async () => {
  const generation = ++kiloGeneration;
  modal('<h2>Kilo 设备授权</h2><p>登录 Kilo 并确认设备码，完成后自动保存独立 Kilo 凭据。</p><p id="kilo-login-status" role="status">正在申请设备码…</p><input id="kilo-code" class="full" readonly aria-label="Kilo 设备码"><div class="modal-actions"><button id="kilo-close" class="secondary">取消</button><button id="kilo-authorize" class="primary" disabled>打开授权页</button></div>');
  $('kilo-close').onclick = closeModal;
  try {
    const start = await api.loginStart('kilo');
    if (generation !== kiloGeneration) { await api.loginCancel(start.session); return; }
    kiloSession = start.session;
    $('kilo-code').value = start.user_code;
    $('kilo-authorize').disabled = false;
    $('kilo-authorize').onclick = () => action('kilo-open-login', () => api.loginOpen(start.session));
    $('kilo-login-status').textContent = '打开授权页并核对设备码；此窗口会自动检查结果。';
    const poll = async () => {
      if (generation !== kiloGeneration) return;
      try {
        const status = await api.loginStatus(start.session);
        if (generation !== kiloGeneration) return;
        if (status.done) {
          kiloSession = null;
          if (status.error || !status.account_id) throw new Error(status.error || 'Kilo 授权未保存');
          state.selected = status.account_id; closeModal(); await refresh(true); launchChannel('kilo');
          await api.preferences({channel:'kilo'}); await loadKilo(status.account_id);
          toast('Kilo 已保存；请检查 ChatGPT 订阅绑定，再测试'); return;
        }
        kiloTimer = setTimeout(poll, 3000);
      } catch (error) {
        if (generation === kiloGeneration) $('kilo-login-status').textContent = error.message;
      }
    };
    void poll();
  } catch (error) { if (generation === kiloGeneration) $('kilo-login-status').textContent = error.message; }
};
$('modal').addEventListener('close', () => {
  kiloGeneration++; clearTimeout(kiloTimer);
  const session = kiloSession; kiloSession = null;
  if (session) void api.loginCancel(session).catch(() => toast('Kilo 授权取消未确认', 'error'));
});
document.querySelector('[data-page="settings"]').addEventListener('click', () => { void loadKilo(); });
