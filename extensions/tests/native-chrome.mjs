// Opt-in isolated-profile acceptance driver. Uses the installed Chrome binary
// through its documented extension-debugging pipe; the browser sandbox stays on.
import { spawn } from 'node:child_process';
import fs from 'node:fs/promises';
import path from 'node:path';
const [chrome, profile, extension, host, page] = process.argv.slice(2);
const manifest = { name: 'io.github.parsania80.fluxget', description: 'FluxGet isolated acceptance host', path: host, type: 'stdio', allowed_origins: ['chrome-extension://phfanjlpchehjklocnblfjdhgmlcjoaa/'] };
for (const dir of [path.join(profile, 'NativeMessagingHosts'), path.join(process.env.XDG_CONFIG_HOME, 'google-chrome/NativeMessagingHosts')]) {
  await fs.mkdir(dir, { recursive: true }); await fs.writeFile(path.join(dir, manifest.name + '.json'), JSON.stringify(manifest));
}
const child = spawn(chrome, ['--headless=new', '--remote-debugging-pipe', '--enable-unsafe-extension-debugging', '--user-data-dir=' + profile, '--no-first-run', '--no-default-browser-check', '--disable-background-networking', 'about:blank'], { stdio: ['ignore', 'ignore', 'pipe', 'pipe', 'pipe'] });
let stderr = '', buffer = '', next = 1;
child.stderr.on('data', b => { stderr = (stderr + b.toString()).slice(-10000); });
const pending = new Map();
child.on("exit", () => { for (const p of pending.values()) p.reject(Error("Test Chrome exited: " + stderr)); pending.clear(); });
child.stdio[3].on("error", () => {});
child.stdio[4].on('data', bytes => {
  buffer += bytes.toString();
  let cut;
  while ((cut = buffer.indexOf('\0')) >= 0) {
    const text = buffer.slice(0, cut); buffer = buffer.slice(cut + 1);
    let message; try { message = JSON.parse(text); } catch { continue; }
    if (pending.has(message.id)) { const p = pending.get(message.id); pending.delete(message.id); message.error ? p.reject(Error(JSON.stringify(message.error))) : p.resolve(message.result); }
  }
});
function send(method, params = {}, sessionId) {
  const id = next++;
  return new Promise((resolve, reject) => { pending.set(id, { resolve, reject }); child.stdio[3].write(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }) + '\0'); });
}
async function evaluate(session, expression) {
  const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true, userGesture: true }, session);
  if (r.exceptionDetails) throw Error(JSON.stringify(r.exceptionDetails)); return r.result.value;
}
async function until(check, label) { const end = Date.now() + 20000; while (Date.now() < end) { const result = await check(); if (result) return result; await new Promise(r => setTimeout(r, 100)); } throw Error(label); }
const timeout = setTimeout(() => { child.kill('SIGTERM'); console.error('Browser acceptance timed out'); process.exitCode = 1; }, 55000);
try {
  await fs.mkdir(path.join(profile, 'downloads'), { recursive: true });
  await send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: path.join(profile, 'downloads'), eventsEnabled: true });
  console.log('Test browser:', (await send('Browser.getVersion')).product);
  const loaded = await send('Extensions.loadUnpacked', { path: extension });
  console.log('Temporary extension loaded:', loaded.id);
  const worker = await until(async () => (await send('Target.getTargets')).targetInfos.find(t => t.type === 'service_worker' && t.url.startsWith('chrome-extension://' + loaded.id + '/')), 'service worker missing');
  const workerSession = (await send('Target.attachToTarget', { targetId: worker.targetId, flatten: true })).sessionId;
  await send('Runtime.enable', {}, workerSession);
  await until(() => evaluate(workerSession, `typeof chrome !== 'undefined' && Boolean(chrome.storage) && typeof native === 'function'`), 'service worker API not initialized');
  const popupTarget = await send('Target.createTarget', { url: 'chrome-extension://' + loaded.id + '/popup.html' });
  const popupSession = (await send('Target.attachToTarget', { targetId: popupTarget.targetId, flatten: true })).sessionId;
  await until(() => evaluate(popupSession, `Boolean(document.getElementById('enabled')) && document.getElementById('status').textContent.includes('Connected')`), 'popup native connection missing');
  await evaluate(popupSession, `document.getElementById('enabled').click()`);
  const mode = process.env.FLUXGET_BROWSER_NATIVE_MODE === 'ask' ? 'Ask first' : 'Automatic capture';
  await until(() => evaluate(popupSession, `document.getElementById('status').textContent.includes(${JSON.stringify(mode)})`), 'popup profile consent was not applied');
  if (process.env.FLUXGET_EXTENSION_POPUP_SCREENSHOT) {
    const shot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true, clip: { x: 0, y: 0, width: 360, height: 820, scale: 1 } }, popupSession);
    await fs.writeFile(process.env.FLUXGET_EXTENSION_POPUP_SCREENSHOT, Buffer.from(shot.data, 'base64'));
  }
  console.log('Real popup native connection and profile consent verified');
  await evaluate(workerSession, `globalThis.acceptanceItems = {}; globalThis.acceptanceErased = []; chrome.downloads.onCreated.addListener(i => { acceptanceItems[new URL(i.url).pathname] = i.id; }); chrome.downloads.onErased.addListener(id => acceptanceErased.push(id));`);
  await until(() => evaluate(workerSession, `chrome.scripting.getRegisteredContentScripts().then(s => s.some(v => v.id === 'fluxget-browser-click'))`), 'Ctrl-click content script not registered');
  const target = await send('Target.createTarget', { url: page + '#download-page' });
  const session = (await send('Target.attachToTarget', { targetId: target.targetId, flatten: true })).sessionId;
  await until(async () => evaluate(session, `document.getElementById('download') !== null`), 'download page missing');
  const point = await evaluate(session, `(() => { const r = document.getElementById('bypass').getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 }; })()`);
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, modifiers: 2, ...point }, session);
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, modifiers: 2, ...point }, session);
  const bypass = await until(async () => (await evaluate(workerSession, 'chrome.downloads.search({})')).find(i => new URL(i.finalUrl || i.url).pathname === '/bypass.iso' && i.state === 'complete'), 'Ctrl-click download did not stay in browser');
  console.log('Trusted Ctrl + left-click browser download:', bypass.state);
  await evaluate(session, `document.getElementById('download').click()`);
  const result = await until(async () => {
    const items = await evaluate(workerSession, 'chrome.downloads.search({})');
    const captured = await evaluate(workerSession, `({ id: acceptanceItems['/redirect.iso'], erased: acceptanceErased })`);
    const state = await evaluate(workerSession, 'chrome.storage.local.get(null)');
    if (state.lastMessage?.includes('needs recovery') || state.lastMessage?.includes('not accept')) throw Error(state.lastMessage);
    return captured.id && captured.erased.includes(captured.id) && !items.some(i => i.id === captured.id) && state.lastMessage === 'Download handed to FluxGet' ? { history: 'handed-off entry erased', message: state.lastMessage } : null;
  }, 'capture did not complete; browser logs: ' + stderr);
  console.log('Real Chrome handoff:', JSON.stringify(result));
  await fetch(page + 'exclude-integration');
  await evaluate(session, `const excluded=document.createElement('a');excluded.href='/excluded.iso';document.body.appendChild(excluded);excluded.click()`);
  const excluded = await until(async () => (await evaluate(workerSession, 'chrome.downloads.search({})')).find(i => i.url.endsWith('/excluded.iso') && i.state === 'complete'), 'excluded host did not stay in browser');
  console.log('Excluded host fallback with all-site access:', excluded.state);
  await fetch(page + 'disable-integration');
  await evaluate(session, `const a=document.createElement('a');a.href='/fallback.iso';document.body.appendChild(a);a.click()`);
  const fallback = await until(async () => (await evaluate(workerSession, 'chrome.downloads.search({})')).find(i => i.url.endsWith('/fallback.iso') && i.state === 'complete'), 'disabled capture did not stay in browser');
  console.log('Master disable fallback:', fallback.state);
  const kept = await evaluate(workerSession, 'chrome.downloads.search({})');
  if (!kept.some(i => i.id === bypass.id && i.state === 'complete')) throw Error('Ctrl-click history was erased');
  if (kept.length !== 3) throw Error('Unexpected browser history cleanup scope');

} finally {
  try { await Promise.race([send('Browser.close'), new Promise((_, reject) => setTimeout(() => reject(Error('close timeout')), 2000))]); } catch { child.kill('SIGTERM'); }
  clearTimeout(timeout);
}
