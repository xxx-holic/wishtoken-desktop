'use strict';
const PROBLEMS = Object.freeze({
  deviceauth_disabled: { message: '此工作区未启用设备码登录。请打开 ChatGPT「设置 → 账户安全与登录」，启用 Codex、Excel、PowerPoint 和 Word 的设备代码登录，再重新授权。组织工作区需由管理员开启。', action: 'chatgptSecurity' },
  auth_page_conflict: { message: '登录会话已被另一项请求改变。本次授权已停止，请结束旧授权后重新开始；不要同时使用多个登录页。' },
});
function loginProblem(code) { return typeof code === 'string' && Object.hasOwn(PROBLEMS, code) ? { code, ...PROBLEMS[code] } : null; }
function watchAuthFailures(page) {
  const result = { problem: null, dispose() {} }; let disposed = false;
  if (typeof page.on !== 'function') return result;
  const listener = async response => {
    try {
      const url = new URL(response.url());
      if (url.origin !== 'https://auth.openai.com' || !url.pathname.startsWith('/api/accounts/') || response.status() < 400) return;
      const raw = await response.text();
      if (disposed || raw.length > 1024 * 1024) return;
      const data = JSON.parse(raw);
      result.problem = loginProblem(typeof data.error === 'string' ? data.error : data.error?.code) || result.problem;
    } catch { /* Never expose arbitrary browser response bodies or credentials. */ }
  };
  page.on('response', listener);
  result.dispose = () => { disposed = true; page.off('response', listener); };
  return result;
}
module.exports = { loginProblem, watchAuthFailures };
