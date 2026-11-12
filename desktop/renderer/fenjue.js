'use strict';
let fenjueSession = null, fenjueTimer, fenjueGeneration = 0;
async function loadFenjue(preferred) {
  const select = $('fenjue-account'), selected = preferred || select.value || state.selected;
  select.innerHTML = (state.data?.accounts || []).filter(a => a.provider === 'fenjue').map(a => `<option value="${esc(a.id)}">${esc(label(a))}</option>`).join('');
  select.value = selected; if (!select.value) select.selectedIndex = 0;
  $('fenjue-check').disabled = !select.value;
  $('fenjue-status').textContent = select.value ? '点击检查，核对已绑定的 ChatGPT 账号和订阅状态。' : '先授权焚诀，或导入 type 为 fenjue 的 JSON。';
}
$('fenjue-setup').onclick = () => action('fenjue-setup', async () => { switchPage('settings'); await loadFenjue(); $('fenjue-settings').scrollIntoView(); });
$('fenjue-byok').onclick = () => action('fenjue-byok', () => api.fenjueOpen());
$('fenjue-account').onchange = () => { $('fenjue-check').disabled = !$('fenjue-account').value; $('fenjue-status').textContent = '请检查当前账号的订阅绑定。'; };
$('fenjue-check').onclick = () => action('fenjue-check', async () => {
  const result = await api.fenjueSubscription($('fenjue-account').value);
  $('fenjue-status').textContent = result.state === 'connected' ? `已绑定 ${result.email || 'ChatGPT 订阅'}${result.usageLimit ? ' · 有历史限额提示，实际可用性以本次请求为准' : ''}` : result.state === 'disconnected' ? '尚未绑定 ChatGPT；请打开订阅设置完成连接。' : '订阅连接异常；请在焚诀设置重新连接。';
});
$('fenjue-login').onclick = async () => {
  const generation = ++fenjueGeneration;
  modal('<h2>焚诀设备授权</h2><p>登录焚诀并确认设备码，完成后自动保存独立焚诀凭据。</p><p id="fenjue-login-status" role="status">正在申请设备码…</p><input id="fenjue-code" class="full" readonly aria-label="焚诀设备码"><div class="modal-actions"><button id="fenjue-close" class="secondary">取消</button><button id="fenjue-authorize" class="primary" disabled>打开授权页</button></div>');
  $('fenjue-close').onclick = closeModal;
  try {
    const start = await api.loginStart('fenjue');
    if (generation !== fenjueGeneration) { await api.loginCancel(start.session); return; }
    fenjueSession = start.session;
    $('fenjue-code').value = start.user_code;
    $('fenjue-authorize').disabled = false;
    $('fenjue-authorize').onclick = () => action('fenjue-open-login', () => api.loginOpen(start.session));
    $('fenjue-login-status').textContent = '打开授权页并核对设备码；此窗口会自动检查结果。';
    const poll = async () => {
      if (generation !== fenjueGeneration) return;
      try {
        const status = await api.loginStatus(start.session);
        if (generation !== fenjueGeneration) return;
        if (status.done) {
          fenjueSession = null;
          if (status.error || !status.account_id) throw new Error(status.error || '焚诀授权未保存');
          state.selected = status.account_id; closeModal(); await refresh(true); launchChannel('fenjue');
          await api.preferences({channel:'fenjue'}); await loadFenjue(status.account_id);
          toast('焚诀已保存；请检查 ChatGPT 订阅绑定，再测试'); return;
        }
        fenjueTimer = setTimeout(poll, 3000);
      } catch (error) {
        if (generation === fenjueGeneration) $('fenjue-login-status').textContent = error.message;
      }
    };
    void poll();
  } catch (error) { if (generation === fenjueGeneration) $('fenjue-login-status').textContent = error.message; }
};
$('modal').addEventListener('close', () => {
  fenjueGeneration++; clearTimeout(fenjueTimer);
  const session = fenjueSession; fenjueSession = null;
  if (session) void api.loginCancel(session).catch(() => toast('焚诀授权取消未确认', 'error'));
});
document.querySelector('[data-page="settings"]').addEventListener('click', () => { void loadFenjue(); });
