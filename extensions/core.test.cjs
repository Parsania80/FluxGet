const test = require('node:test');
const assert = require('node:assert/strict');
const { Evidence, Handoff, publicURL, BrowserBypass } = require('./src/core.js');
const item = () => ({ id: 1, url: 'https://files.example.org/image.iso', finalUrl: 'https://files.example.org/image.iso', filename: '/tmp/image.iso', state: 'in_progress', paused: false, danger: 'safe', incognito: false });
function fixture(change = {}) {
  const browser = item(), actions = [], records = new Map(); let receiptActive = false;
  const api = {
    uuid: () => 'request-1234567890', enabled: async () => true,
    save: async r => { records.set(r.request_id, structuredClone(r)); actions.push('save:' + r.stage); },
    remove: async id => { records.delete(id); actions.push('remove'); }, notice: async () => {},
    item: async () => ({ ...browser }),
    pause: async () => { browser.paused = true; actions.push('pause'); },
    resume: async () => { browser.paused = false; actions.push('resume'); },
    erase: async id => { assert.equal(id, browser.id); actions.push('erase'); },
    cancel: async () => { browser.state = 'interrupted'; browser.error = 'USER_CANCELED'; actions.push('cancel'); },
    native: async r => { actions.push(r.operation); if (r.operation === 'policy') return { ok: true, enabled: true, mode: 'automatic' }; if (r.operation === 'activate') receiptActive = true; return { ok: true, stage: receiptActive ? 'active' : 'pending' }; }, ...change
  };
  return { api, browser, actions, records, flow: new Handoff(api) };
}
const proof = { method: 'GET', headers_observed: true, safe: true, credentials: false, private: false };
test('accepted destination precedes cancellation and activation', async () => {
  const f = fixture(); await f.flow.capture(f.browser, proof);
  assert(f.actions.indexOf('pause') < f.actions.indexOf('prepare'));
  assert(f.actions.indexOf('prepare') < f.actions.indexOf('cancel'));
  assert(f.actions.indexOf('save:canceling') < f.actions.indexOf('cancel'));
  assert(f.actions.indexOf('cancel') < f.actions.indexOf('activate'));
  assert(f.actions.indexOf('activate') < f.actions.indexOf('save:cleanup'));
  assert(f.actions.indexOf('save:cleanup') < f.actions.indexOf('erase'));
  assert.equal(f.records.size, 0);
});
test('history cleanup failure retries without activating or canceling again', async () => {
  const f = fixture(); let failed = true;
  f.api.erase = async () => { f.actions.push('erase'); if (failed) { failed = false; throw Error('history unavailable'); } };
  await f.flow.capture(f.browser, proof);
  assert.equal([...f.records.values()][0].stage, 'cleanup');
  await f.flow.recover([...f.records.values()]);
  assert.equal(f.records.size, 0); assert.equal(f.actions.filter(a => a === 'activate').length, 1);
  assert.equal(f.actions.filter(a => a === 'cancel').length, 1); assert.equal(f.actions.filter(a => a === 'erase').length, 2);
});
test('crash after history erasure recovers missing browser item from confirmed ownership', async () => {
  const f = fixture(); let missing = false;
  f.api.item = async () => missing ? undefined : ({ ...f.browser });
  f.api.erase = async () => { missing = true; throw Error('erase acknowledgment lost'); };
  await f.flow.capture(f.browser, proof); assert.equal(f.records.size, 1);
  await f.flow.recover([...f.records.values()]);
  assert.equal(f.records.size, 0); assert.equal(f.actions.filter(a => a === 'activate').length, 1);
});
test('cleanup never erases browser entries whose state changed', async () => {
  for (const state of ['in_progress', 'complete']) {
    const f = fixture(); f.browser.state = state;
    await f.flow.cleanup({ item: 1, request_id: 'request-1234567890' });
    assert(!f.actions.includes('erase'));
  }
});
test('cleanup recovery cannot erase an entry without confirmed FluxGet ownership', async () => {
  const f = fixture(); f.browser.state = 'interrupted'; f.browser.error = 'USER_CANCELED';
  const record = { item: 1, kind: 'capture', request_id: 'request-1234567890', stage: 'cleanup' };
  await f.api.save(record); await f.flow.recover([record]);
  assert(!f.actions.includes('erase')); assert.equal(f.records.size, 1);
});
test('Ctrl-click bypass is single-use and matches the originating tab and redirect URL', () => {
  const b = item(); b.finalUrl = 'https://cdn.example.org/image.iso?token=preserved';
  const r = { urls: [b.url, b.finalUrl], final: b.finalUrl, started: 1000, tab: 2 };
  const bypass = new BrowserBypass([], () => 1000);
  const ticket = { id: 'click-123', tab: 1, url: b.url, at: 1000 };
  bypass.add(ticket);
  assert.equal(bypass.take(b, [r], [2]), false);
  assert.equal(bypass.take({ ...b, url: 'https://other.example.org/file' }, [r], [1, 2]), false);
  assert.equal(bypass.take(b, [r], [2, 1]), true);
  bypass.add(ticket); assert.equal(bypass.take(b, [r], [2, 1]), false);
  const restored = new BrowserBypass(JSON.parse(JSON.stringify(bypass.tickets)), () => 1000);
  assert.equal(restored.take(b, [r], [2, 1]), false);
});
test('Ctrl-click bypass expires, is bounded, rejects old requests and handles buttons', () => {
  let now = 1000; const b = item(), r = { urls: [b.url], final: b.finalUrl, started: 1000 };
  const bypass = new BrowserBypass([], () => now);
  bypass.add({ id: 'button', tab: 1, url: '', at: now });
  assert.equal(bypass.take(b, [{ ...r, started: 800 }], [1]), false);
  now = 6001; assert.equal(bypass.take(b, [r], [1]), false);
  for (let i = 0; i < 40; i++) bypass.add({ id: 'click-' + i, tab: 1, url: b.url, at: now });
  assert.equal(bypass.tickets.length, 32);
  now += 30001; assert.equal(bypass.take(b, [r], [1]), false);
  bypass.add({ id: 'button-2', tab: 1, url: '', at: now });
  assert.equal(bypass.take(b, [{ ...r, started: now }], [1]), true);
});
test('content script observes only trusted Ctrl-left gestures without changing navigation', async () => {
  const vm = require('node:vm'), fs = require('node:fs'), listeners = {}, messages = [];
  let now = 1000;
  const context = vm.createContext({ URL, Date: { now: () => now }, crypto: { randomUUID: () => 'gesture-123' }, document: { addEventListener: (event, fn) => { listeners[event] = fn; } }, chrome: { runtime: { sendMessage: async m => messages.push(m) } } });
  vm.runInContext(fs.readFileSync(require.resolve('./src/click.js'), 'utf8'), context);
  const event = { isTrusted: true, ctrlKey: true, button: 0, type: 'pointerdown', composedPath: () => [{ matches: () => true, href: item().url }], preventDefault: () => assert.fail('native click behavior changed') };
  listeners.pointerdown({ ...event, isTrusted: false }); listeners.pointerdown({ ...event, ctrlKey: false }); listeners.pointerdown({ ...event, button: 2 });
  assert.equal(messages.length, 0);
  listeners.pointerdown(event); now += 2000; listeners.click({ ...event, type: 'click' });
  assert.equal(messages.length, 2); assert.equal(messages[0].id, messages[1].id); assert.equal(messages[0].url, item().url);
});
test('prepare rejection leaves browser resumable without starting FluxGet', async () => {
  const f = fixture(); const native = f.api.native;
  f.api.native = async r => r.operation === 'prepare' ? { ok: false, error: 'disk full' } : native(r);
  await f.flow.capture(f.browser, proof);
  assert.equal(f.browser.paused, false); assert(!f.actions.includes('cancel')); assert(!f.actions.includes('activate')); assert(f.actions.includes('abort'));
});
test('disable while prepared abandons the handoff', async () => {
  const f = fixture(); const native = f.api.native; let calls = 0;
  f.api.native = async r => r.operation === 'policy' && ++calls === 2 ? { ok: true, enabled: false } : native(r);
  await f.flow.capture(f.browser, proof);
  assert(f.actions.includes('resume')); assert(!f.actions.includes('cancel')); assert(!f.actions.includes('activate'));
});
test('lost activation acknowledgment recovers the same request', async () => {
  const f = fixture(); const native = f.api.native; let lost = true;
  f.api.native = async r => { if (r.operation === 'activate' && lost) { lost = false; throw Error('lost acknowledgment'); } return native(r); };
  await f.flow.capture(f.browser, proof);
  assert.equal(f.records.size, 1); assert.equal(f.browser.state, 'interrupted');
  assert(!f.actions.includes('erase'));
  await f.flow.recover([...f.records.values()]);
  assert.equal(f.records.size, 0); assert.equal(f.actions.filter(a => a === 'cancel').length, 1); assert(!f.actions.includes('resume'));
});
test('crash after intent but before browser cancel resumes browser', async () => {
  const f = fixture(); f.browser.paused = true;
  const r = { item: 1, kind: 'capture', request_id: 'request-1234567890', stage: 'canceling' };
  await f.api.save(r); await f.flow.recover([r]);
  assert(f.actions.includes('abort')); assert(f.actions.includes('resume')); assert(!f.actions.includes('activate'));
});
test('completed or missing browser state never guesses a new transfer', async () => {
  for (const state of ['complete', 'missing']) {
    const f = fixture(); f.browser.state = state;
    if (state === 'missing') f.api.item = async () => undefined;
    const r = { item: 1, kind: 'capture', request_id: 'request-1234567890', stage: 'canceling' };
    await f.api.save(r); await f.flow.recover([r]); assert(!f.actions.includes('activate'));
    if (state === 'missing') assert.equal(f.records.size, 1);
  }
});
test('unknown policy and manual capture mode never pause', async () => {
  for (const reply of [{ enabled: false }, { enabled: true, mode: 'manual' }]) {
    const f = fixture({ native: async () => reply }); await f.flow.capture(f.browser, proof); assert(!f.actions.includes('pause'));
  }
});
test('POST, cookies, redirected credentials and ambiguous traffic stay in browser', () => {
  const good = () => {
    const e = new Evidence(() => 1000); e.start({ requestId: '1', url: item().url, method: 'GET' });
    e.headers({ requestId: '1', requestHeaders: [] }); e.response({ requestId: '1', url: item().url, statusCode: 200 }); return e;
  };
  assert.deepEqual(good().proof(item()), proof);
  for (const bad of [b => b.incognito = true, b => delete b.incognito, b => b.danger = 'accepted', b => b.danger = 'asyncScanning', b => b.paused = true, b => b.state = 'complete', b => b.url += '#fragment']) {
    const b = item(); bad(b); assert.equal(good().proof(b), null);
  }
  const post = good(); post.start({ requestId: '1', url: item().url, method: 'POST' }); post.headers({ requestId: '1', requestHeaders: [] }); post.response({ requestId: '1', url: item().url, statusCode: 200 }); assert.equal(post.proof(item()), null);
  const cookie = good(); cookie.headers({ requestId: '1', requestHeaders: [{ name: 'Cookie', value: 'never retained' }] });
  cookie.start({ requestId: '1', url: item().url, method: 'GET' }); cookie.headers({ requestId: '1', requestHeaders: [] }); cookie.response({ requestId: '1', url: item().url, statusCode: 200 });
  assert.equal(cookie.proof(item()), null); assert(!JSON.stringify([...cookie.requests.values()]).includes('never retained'));
  const ambiguous = good(); ambiguous.start({ requestId: '2', url: item().url, method: 'GET' }); assert.equal(ambiguous.proof(item()), null);
  assert(!publicURL('blob:https://example.org/file')); assert(!publicURL('https://user:pass@example.org/file')); assert(publicURL('https://example.org/file?'));
});
test('query-bearing CDN redirects remain exact and eligible without credentials', async () => {
  const e = new Evidence();
  const b = item(); b.url += '?token=initial'; b.finalUrl = 'https://cdn.example.org/image.iso?token=private%2Bvalue&part=1&part=2';
  e.start({ requestId: '1', url: b.url, method: 'GET' }); e.headers({ requestId: '1', requestHeaders: [] });
  e.redirect({ requestId: '1', redirectUrl: b.finalUrl });
  e.start({ requestId: '1', url: b.finalUrl, method: 'GET' }); e.headers({ requestId: '1', requestHeaders: [] });
  e.response({ requestId: '1', url: b.finalUrl, statusCode: 200 });
  assert.deepEqual(e.proof(b), proof);
  const f = fixture(); Object.assign(f.browser, b); const native = f.api.native; let preparedURL;
  f.api.native = async r => { if (r.operation === 'prepare') preparedURL = r.url; return native(r); };
  await f.flow.capture(f.browser, e.proof(b)); assert.equal(preparedURL, b.finalUrl); assert(f.actions.includes('activate'));
  e.headers({ requestId: '1', requestHeaders: [{ name: 'Cookie', value: 'not-retained' }] });
  assert.equal(e.proof(b), null); assert.match(e.inspect(b).reason, /cookies/);
});
test('capture failures show a reason while redacting query-bearing URLs', async () => {
  const f = fixture(); const native = f.api.native; const messages = [];
  f.api.notice = async message => messages.push(message);
  f.api.native = async r => r.operation === 'prepare' ? { ok: false, error: 'GET https://cdn.example.org/private-path/file?token=do-not-log failed' } : native(r);
  await f.flow.capture(f.browser, proof);
  assert(messages.some(m => m.includes('failed'))); assert(messages.every(m => !m.includes('do-not-log') && !m.includes('private-path')));
  assert(!f.actions.includes('cancel')); assert.equal(f.browser.paused, false);
  const e = new Evidence(); assert.match(e.inspect(item()).reason, /not observed/);
});
test('explicit manual link recovery has no browser cancellation', async () => {
  const f = fixture(); const r = { item: 'link:request-1234567890', request_id: 'request-1234567890', kind: 'link', stage: 'link-activating' };
  await f.api.save(r); await f.flow.recover([r]); assert(f.actions.includes('activate')); assert(!f.actions.includes('cancel')); assert.equal(f.records.size, 0);
});
test('removed destination acceptance resumes without browser cancellation', async () => {
  const f = fixture(); const native = f.api.native;
  f.api.native = async r => r.operation === 'status' ? { ok: false, error: 'destination removed' } : native(r);
  await f.flow.capture(f.browser, proof); assert(f.actions.includes('resume')); assert(!f.actions.includes('cancel'));
});
test('an unseen original redirect request is not assumed to be a public GET', () => {
  const e = new Evidence(); const redirected = item(); redirected.url = 'https://page.example.org/redirect';
  e.start({ requestId: '1', url: redirected.finalUrl, method: 'GET' }); e.headers({ requestId: '1', requestHeaders: [] }); e.response({ requestId: '1', url: redirected.finalUrl, statusCode: 200 });
  assert.equal(e.proof(redirected), null);
});
test('site permission revocation before cancel returns ownership to browser', async () => {
  const f = fixture(); let calls = 0; f.api.enabled = async () => ++calls === 1;
  await f.flow.capture(f.browser, proof); assert(f.actions.includes('resume')); assert(!f.actions.includes('cancel'));
});
test('concurrent startup requests share one persistent profile identity', async () => {
  const vm = require('node:vm'), fs = require('node:fs'); let generated = 0, saved = 0;
  const event = () => ({ addListener() {} });
  const chrome = { storage: { local: { get: async () => ({}), set: async () => { saved++; } } },
    runtime: { onInstalled: event(), onStartup: event(), onMessage: event() }, permissions: { onAdded: event(), onRemoved: event() },
    downloads: { onCreated: event() }, contextMenus: { onClicked: event() }, alarms: { onAlarm: event() } };
  const context = vm.createContext({ chrome, FLUXGET_BROWSER: 'chrome', FluxGetCore: require('./src/core.js'), crypto: { randomUUID: () => { generated++; return 'profile-1234567890'; } } });
  vm.runInContext(fs.readFileSync(require.resolve('./src/background.js'), 'utf8'), context);
  const result = await vm.runInContext('Promise.all([identity(),identity(),identity()])', context);
  assert.equal(new Set(result).size, 1); assert.equal(generated, 1); assert.equal(saved, 1);
});
function popupFixture({ mode = 'ask', access = 'none', consent = true, approved = true, connected, integration = true } = {}) {
  const vm = require('node:vm'), fs = require('node:fs'), elements = new Map(), calls = [];
  const document = { getElementById: id => {
    if (!elements.has(id)) elements.set(id, { textContent: '', hidden: false, addEventListener(event, fn) { this[event] = fn; } });
    return elements.get(id);
  } };
  const chrome = { runtime: { sendMessage: async message => {
    calls.push(message.action);
    if (message.action === 'profile') {
      consent = message.consent;
      if (connected !== undefined) connected = consent;
    }
    return message.action === 'status' ? { consent, remote: { ok: true, enabled: integration && (connected ?? consent), connected, mode }, pending: 0 } : { ok: true };
  } }, permissions: {
    getAll: async () => ({ permissions: access === 'none' ? [] : ['webRequest', 'scripting'], origins: access === 'all' ? ['http://*/*', 'https://*/*'] : access === 'limited' ? ['https://files.example.org/*'] : [] }),
    contains: async value => { assert.deepEqual(JSON.parse(JSON.stringify(value)), { permissions: ['webRequest', 'scripting'], origins: ['http://*/*', 'https://*/*'] }); return access === 'all'; },
    request: async value => { calls.push('request'); assert.deepEqual(JSON.parse(JSON.stringify(value)), { permissions: ['webRequest', 'scripting'], origins: ['http://*/*', 'https://*/*'] }); if (approved) access = 'all'; return approved; },
    remove: async () => { calls.push('remove'); access = 'none'; return true; }
  } };
  const context = vm.createContext({ document, chrome });
  vm.runInContext(fs.readFileSync(require.resolve('./src/popup.js'), 'utf8'), context);
  return { elements, calls, ready: () => vm.runInContext('refresh()', context) };
}
test('popup reflects desktop disconnection and lets the user reconnect', async () => {
  const f = popupFixture({ mode: 'manual', consent: true, connected: false }); await f.ready();
  assert.equal(f.elements.get('enabled').checked, false);
  assert.match(f.elements.get('status').textContent, /connect to FluxGet/);
  f.elements.get('enabled').checked = true; await f.elements.get('enabled').change();
  assert.equal(f.elements.get('enabled').checked, true);
  const paused = popupFixture({ connected: true, integration: false }); await paused.ready();
  assert.equal(paused.elements.get('enabled').checked, true);
  assert.match(paused.elements.get('status').textContent, /Profile connected/);
});
test('popup distinguishes all-site access, limited access and missing permission', async () => {
  for (const mode of ['ask', 'automatic', 'manual']) {
    for (const access of ['none', 'limited', 'all']) {
      const f = popupFixture({ mode, access }); await f.ready();
      assert.equal(f.elements.get('setup').hidden, mode === 'manual' || access === 'all');
      assert.equal(f.elements.get('grant').hidden, access === 'all');
      assert.equal(f.elements.get('revoke').hidden, access === 'none');
      if (access === 'all') assert.match(f.elements.get('scope').textContent, /New sites do not need separate approval/);
      if (access === 'none' && mode !== 'manual') assert.match(f.elements.get('status').textContent, /Site access required/);
      if (access === 'limited' && mode !== 'manual') assert.match(f.elements.get('status').textContent, /Capture setup incomplete/);
    }
  }
});
test('enabling a capture profile requests all websites before profile consent', async () => {
  const f = popupFixture({ consent: false }); await f.ready(); f.calls.length = 0;
  f.elements.get('enabled').checked = true; await f.elements.get('enabled').change();
  assert.deepEqual(f.calls.slice(0, 3), ['request', 'permission', 'profile']);
  assert.equal(f.elements.get('enabled').checked, true); assert.equal(f.elements.get('grant').hidden, true);
});
test('denied all-site permission does not enable a new profile', async () => {
  const f = popupFixture({ consent: false, approved: false }); await f.ready(); f.calls.length = 0;
  f.elements.get('enabled').checked = true; await f.elements.get('enabled').change();
  assert(!f.calls.includes('profile')); assert.equal(f.elements.get('enabled').checked, false);
  assert.match(f.elements.get('message').textContent, /not granted/);
});
test('existing profiles can grant all websites and revoke access without losing consent', async () => {
  const f = popupFixture({ access: 'limited' }); await f.ready();
  await f.elements.get('grant').onclick(); assert.equal(f.elements.get('grant').hidden, true);
  await f.elements.get('revoke').onclick(); assert.equal(f.elements.get('grant').hidden, false);
  assert.equal(f.elements.get('enabled').checked, true); assert.match(f.elements.get('status').textContent, /Site access required/);
});
test('manual-only profile enable and profile disable need no website permission', async () => {
  const f = popupFixture({ mode: 'manual', consent: false }); await f.ready(); f.calls.length = 0;
  f.elements.get('enabled').checked = true; await f.elements.get('enabled').change();
  assert(!f.calls.includes('request')); assert.equal(f.elements.get('enabled').checked, true);
  f.elements.get('enabled').checked = false; await f.elements.get('enabled').change();
  assert(!f.calls.includes('request')); assert.equal(f.elements.get('enabled').checked, false);
});
