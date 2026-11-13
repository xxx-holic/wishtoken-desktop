'use strict';
let fenjueSession = null, fenjueTimer, fenjueGeneration = 0;
let bindingSession = null, bindingTimer, bindingGeneration = 0;
async function loadFenjue(preferred) {
  const select = $('fenjue-account'), selected = preferred || select.value || state.selected;
  select.innerHTML = (state.data?.accounts || []).filter(a => a.provider === 'fenjue').map(a => `<option value="${esc(a.id)}">${esc(label(a))}</option>`).join('');
  select.value = selected; if (!select.value) select.selectedIndex = 0;
  $('fenjue-check').disabled = !select.value;
  $('fenjue-status').textContent = select.value ? '点击检查，核对已绑定的 ChatGPT 账号和订阅状态。' : '先授权焚诀，或导入 type 为 fenjue 的 JSON。';
}
$('fenjue-setup').onclick = () => action('fenjue-setup', async () => { switchPage('settings'); await loadFenjue(); $('fenjue-settings').scrollIntoView(); });
$('fenjue-byok').onclick = () => action('fenjue-byok', () => api.fenjueOpen());
$('fenjue-bind').onclick = () => {
  const account = $('fenjue-account').value;
  if (!account) { toast('请先选择焚诀账号', 'error'); return; }
  const generation = ++bindingGeneration;
  modal(`<h2 id="binding-title">订阅协议绑定</h2>
    <p>输入一个账号的账密和 2FA 种子，优先走纯协议授权；遇到页面防护或工作区选择时可切换浏览器备用。</p>
    <label class="field-label" for="binding-mode">授权方式</label>
    <select id="binding-mode" class="full"><option value="protocol">协议授权（首选，不打开浏览器）</option><option value="browser">浏览器备用</option></select>
    <label class="field-label" for="binding-proxy">本次授权代理（可选）</label><input id="binding-proxy" class="full" spellcheck="false" placeholder="留空使用连接设置中的代理">
    <div class="login-input-label"><label class="field-label" for="binding-input">账号资料</label><label><input type="checkbox" id="binding-reveal"> 显示输入</label></div>
    <textarea id="binding-input" class="login-secret" spellcheck="false" autocomplete="off" autocapitalize="off" placeholder="邮箱----密码----2FA种子"></textarea>
    <p class="login-hint">密码和种子只在本次授权内存留，完成或取消后立即清空。</p>
    <p id="binding-error" class="danger-text" role="alert" hidden></p>
    <section id="binding-results" aria-label="订阅绑定进度" hidden><div class="login-summary"><strong id="binding-summary" role="status"></strong></div><ol id="binding-rows" class="login-queue"></ol></section>
    <div class="modal-actions"><button class="secondary" id="binding-close">取消</button><button class="secondary" id="binding-open" hidden>查看授权浏览器</button><button class="primary" id="binding-start">开始绑定</button></div>`);
  $('modal').setAttribute('aria-labelledby', 'binding-title');
  $('binding-proxy').value = state.data?.settings?.proxy_url || '';
  $('binding-reveal').onchange = () => $('binding-input').classList.toggle('login-secret', !$('binding-reveal').checked);
  $('binding-close').onclick = closeModal;
  $('binding-open').onclick = () => action('binding-open', () => api.subscriptionBindingOpen(bindingSession));
  $('binding-start').onclick = async () => {
    $('binding-start').disabled = true; $('binding-error').hidden = true;
    try {
      const result = await api.subscriptionBindingStart({ account_id: account, mode: $('binding-mode').value, proxy_url: $('binding-proxy').value, text: $('binding-input').value });
      if (generation !== bindingGeneration) { await api.subscriptionBindingCancel(result.id); return; }
      $('binding-input').value = ''; bindingSession = result.id; $('binding-mode').disabled = true; $('binding-proxy').disabled = true; $('binding-start').hidden = true; $('binding-results').hidden = false;
      const poll = async () => {
        if (generation !== bindingGeneration) return;
        try {
          const status = await api.subscriptionBindingStatus(bindingSession);
          if (generation !== bindingGeneration) return;
          const row = status.rows?.[0]; $('binding-summary').textContent = status.done ? (row?.status === 'completed' ? '绑定完成' : '绑定未完成') : '正在处理…';
          $('binding-rows').innerHTML = row ? `<li class="login-queue-row" data-status="${esc(row.status)}"><div class="login-row-heading"><strong>${esc(row.email)}</strong><span class="login-state">${esc(row.status === 'completed' ? '已完成' : row.status === 'failed' ? '未完成' : row.status === 'cancelled' ? '已取消' : '处理中')}</span></div><p>${esc(row.message || '')}</p>${row.bound_email ? `<small>已核对：${esc(row.bound_email)}</small>` : ''}${row.cleanup_warning ? `<p class="danger-text">${esc(row.cleanup_warning)}</p>` : ''}</li>` : '';
          $('binding-open').hidden = status.done || status.mode !== 'browser';
          if (status.done) { bindingSession = null; await loadFenjue(account); if (row?.status === 'completed') toast('订阅协议授权已完成，请检查绑定状态'); return; }
          bindingTimer = setTimeout(poll, 800);
        } catch (error) { if (generation === bindingGeneration) { $('binding-error').textContent = error.message; $('binding-error').hidden = false; bindingTimer = setTimeout(poll, 2000); } }
      };
      void poll();
    } catch (error) { $('binding-error').textContent = error.message; $('binding-error').hidden = false; $('binding-start').disabled = false; }
  };
  $('binding-input').focus();
};
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
  bindingGeneration++; clearTimeout(bindingTimer);
  const binding = bindingSession; bindingSession = null;
  const input = $('binding-input'); if (input) input.value = '';
  if (binding) void api.subscriptionBindingCancel(binding).catch(() => toast('订阅授权取消未确认', 'error'));
});
document.querySelector('[data-page="settings"]').addEventListener('click', () => { void loadFenjue(); });
