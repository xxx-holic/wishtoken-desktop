'use strict';
const { _electron: electron, expect } = require('@playwright/test');
const fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os'), net = require('node:net');
const { externalLink, businessCode } = require('../lib/links.cjs');
const root = path.resolve(__dirname, '../..');
(async () => {
  const home = await fs.mkdtemp(path.join(os.tmpdir(), 'wishtoken-business-ui-'));
  const sock = net.createServer();
  await new Promise(resolve => sock.listen(0, '127.0.0.1', resolve));
  const port = sock.address().port;
  await new Promise(resolve => sock.close(resolve));
  await fs.writeFile(path.join(home, 'config.json'), JSON.stringify({ listen: `127.0.0.1:${port}`, api_key: 'synthetic-business-key', auto_refresh: false, usage_probe: false }));
  const app = await electron.launch({ executablePath: process.env.GPTBRIDGE_TEST_APP || require('electron'), args: process.env.GPTBRIDGE_TEST_APP ? [] : [path.join(root, 'desktop')], env: { ...process.env, GPTBRIDGE_DESKTOP_HOME: home, GPTBRIDGE_MAIN_CODEX_HOME: path.join(home, 'unused-main') } });
  try {
    const page = await app.firstWindow(), errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await expect(page.locator('#service-label')).toHaveText('本地服务运行中');
    await app.evaluate(({ shell }) => { globalThis.businessLinks = []; shell.openExternal = async url => { globalThis.businessLinks.push(url); }; });
    await page.locator('[data-page="business"]').click();
    await expect(page.locator('#page-title')).toHaveText('商城与合作');
    await expect(page.locator('#page-business')).toBeVisible();
    await expect(page.locator('#import-files')).toBeHidden();
    const names = await page.locator('#page-business [data-link]').evaluateAll(nodes => nodes.map(node => node.dataset.link));
    for (const name of names) {
      await page.locator(`#page-business [data-link="${name}"]`).click();
      await expect.poll(() => app.evaluate(() => globalThis.businessLinks.at(-1))).toBe(externalLink(name));
    }
    // Intercept only clipboard writes; do not replace the user's clipboard.
    await app.evaluate(({ clipboard }) => { clipboard.writeText = text => { globalThis.businessCopied = text; }; });
    for (const name of ['referral', 'qq']) {
      await page.locator(`[data-business-code="${name}"]`).click();
      await expect.poll(() => app.evaluate(() => globalThis.businessCopied)).toBe(businessCode(name));
    }
    const rejected = await page.evaluate(async () => {
      const checks = [() => window.bridge.openLink('https://example.test'), () => window.bridge.copyBusinessCode('arbitrary')];
      return Promise.all(checks.map(async run => { try { await run(); return false; } catch { return true; } }));
    });
    if (!rejected.every(Boolean)) throw Error('arbitrary IPC input accepted');
    const dest = path.join(root, 'build/validation'); await fs.mkdir(dest, { recursive: true });
    for (const [theme, width, height] of [['light', 1400, 940], ['dark', 980, 690]]) {
      await app.evaluate(({ BrowserWindow }, [w, h]) => BrowserWindow.getAllWindows()[0].setSize(w, h), [width, height]);
      await page.evaluate(theme => { applyTheme(theme); window.scrollTo(0, 0); document.getElementById('toasts').replaceChildren(); }, theme);
      const contentWidth = await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].getContentSize()[0]);
      await expect.poll(() => page.evaluate(() => window.innerWidth)).toBe(contentWidth);
      await page.evaluate(async () => {
        await document.fonts.ready;
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
        await Promise.all(document.getAnimations().map(animation => animation.finished.catch(() => {})));
      });
      const layout = await page.evaluate(() => {
        const side = document.querySelector('.sidebar'), button = document.querySelector('#page-business .secondary');
        return { overflow: document.documentElement.scrollWidth > innerWidth, sidebarScroll: side.scrollHeight > side.clientHeight, sideHeight: side.clientHeight, sideScroll: side.scrollHeight, footerBottom: side.querySelector('footer').getBoundingClientRect().bottom, buttonBackground: getComputedStyle(button).backgroundColor };
      });
      await page.screenshot({ path: path.join(dest, `business-${theme}.png`), fullPage: true });
      if (layout.overflow || layout.sidebarScroll) throw Error(`layout overflow: ${JSON.stringify({ theme, ...layout })}`);
    }
    await page.locator('[data-page="accounts"]').click();
    await expect(page.locator('#page-accounts')).toBeVisible();
    await expect(page.locator('#import-files')).toBeVisible();
    if (errors.length) throw Error(errors.join('\n'));
    console.log(JSON.stringify({ passed: true, synthetic: true, linkCount: names.length, codeCopy: true, themes: ['light', 'dark'], minWindow: '980x690', rendererErrors: 0 }));
  } finally {
    const { BridgeService } = require('../lib/service.cjs'); const svc = new BridgeService({ home, binary: '' });
    svc.config = JSON.parse(await fs.readFile(path.join(home, 'config.json'), 'utf8')); await svc.stop();
    if (process.platform === 'win32') require('node:child_process').execFileSync('taskkill.exe', ['/PID', String(app.process().pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' }); else app.process().kill('SIGKILL');
    const resolved = path.resolve(home);
    if (resolved.startsWith(path.resolve(os.tmpdir()) + path.sep) && path.basename(resolved).startsWith('wishtoken-business-ui-')) await fs.rm(resolved, { recursive: true, force: true, maxRetries: 3 });
  }
})().catch(error => { console.error(error.message); process.exitCode = 1; });
