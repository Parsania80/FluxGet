const test = require('node:test');
const assert = require('node:assert/strict');
const { Evidence, Handoff, publicURL, BrowserBypass, verifyPublicDownload } = require('./src/core.js');
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
test('closing the prompt cancels and erases a captured Chrome transfer without starting FluxGet', async () => {
  const f = fixture(), native = f.api.native;
  f.api.native = async r => r.operation === 'prepare' ? { ok: false, stage: 'canceled' } : native(r);
  await f.flow.capture(f.browser, proof);
  assert(f.actions.includes('save:dismissed'));
  assert(f.actions.indexOf('save:dismissed') < f.actions.indexOf('cancel'));
  assert(f.actions.includes('cancel')); assert(f.actions.includes('erase'));
  assert(!f.actions.includes('resume')); assert(!f.actions.includes('activate'));
  assert.equal(f.records.size, 0);
});
test('dismissal recovery retries failed cleanup and never resumes or activates', async () => {
  for (const failure of ['cancel', 'erase']) {
    const f = fixture(), native = f.api.native, original = f.api[failure];
    let failed = false;
    f.api.native = async r => r.operation === 'prepare' ? { ok: false, stage: 'canceled' } : native(r);
    f.api[failure] = async id => { if (!failed) { failed = true; throw Error('temporary browser failure'); } return original(id); };
    await f.flow.capture(f.browser, proof);
    assert.equal([...f.records.values()][0].stage, 'dismissed');
    await f.flow.recover([...f.records.values()]);
    assert.equal(f.records.size, 0);
    assert(!f.actions.includes('resume')); assert(!f.actions.includes('activate'));
    assert(f.actions.includes('erase'));
  }
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
  assert.equal(bypass.take(b, [{ ...r, started: 700 }], [1]), false);
  now = 6001; assert.equal(bypass.take(b, [r], [1]), false);
  for (let i = 0; i < 40; i++) bypass.add({ id: 'click-' + i, tab: 1, url: b.url, at: now });
  assert.equal(bypass.tickets.length, 32);
  now += 30001; assert.equal(bypass.take(b, [r], [1]), false);
  bypass.add({ id: 'button-2', tab: 1, url: '', at: now });
  assert.equal(bypass.take(b, [{ ...r, started: now }], [1]), true);
});
test('Ctrl-click identifies a redirected download when Chrome drops its tab association', () => {
  const b = item();
  const e = new Evidence(() => 1000);
  e.start({ requestId: 'file', tabId: 4, url: b.url, method: 'GET' });
  e.headers({ requestId: 'file', requestHeaders: [] });
  e.start({ requestId: 'file', tabId: -1, url: b.finalUrl, method: 'GET' });
  e.headers({ requestId: 'file', requestHeaders: [] });
  e.response({ requestId: 'file', url: b.finalUrl, statusCode: 200 });
  assert.equal(e.inspect(b).request.tab, 4);
  const bypass = new BrowserBypass([], () => 1000);
  bypass.add({ id: 'ctrl-link', tab: 4, url: b.url, at: 1000 });
  assert.equal(bypass.take(b, [{ urls: [b.url], final: b.finalUrl, started: 950, tab: -1 }], [-1]), true);
  const other = new BrowserBypass([], () => 1000);
  other.add({ id: 'ctrl-button', tab: 4, url: '', at: 1000 });
  assert.equal(other.take(b, [{ urls: [b.url], final: b.finalUrl, started: 1000, tab: -1 }], [-1]), false);
});
test('content script observes only trusted Ctrl-left gestures without changing navigation', async () => {
  const vm = require('node:vm'), fs = require('node:fs'), listeners = {}, messages = [];
  let now = 1000;
  const context = vm.createContext({ URL, Date: { now: () => now }, crypto: { randomUUID: () => 'gesture-123' }, document: { addEventListener: (event, fn) => { (listeners[event] ||= []).push(fn); } }, chrome: { runtime: { id: 'extension-id', sendMessage: async m => messages.push(m) } } });
  vm.runInContext(fs.readFileSync(require.resolve('./src/click.js'), 'utf8'), context);
  const event = { isTrusted: true, ctrlKey: true, button: 0, type: 'pointerdown', composedPath: () => [{ matches: () => true, href: item().url }], preventDefault: () => assert.fail('native click behavior changed') };
  const emit = e => listeners[e.type].forEach(fn => fn(e));
  emit({ ...event, isTrusted: false }); emit({ ...event, ctrlKey: false }); emit({ ...event, button: 2 });
  assert.equal(messages.length, 0);
  emit(event); now += 2000; emit({ ...event, type: 'click' });
  assert.equal(messages.length, 2); assert.equal(messages[0].id, messages[1].id); assert.equal(messages[0].url, item().url);
  emit(event);
  emit({ ...event, type: 'click', ctrlKey: false });
  assert.equal(messages.length, 4);
  assert(messages.every(message => message.action === 'browser-click'));
});
test('stale extension contexts release page handlers and handle synchronous message failures', async () => {
  const vm = require('node:vm'), fs = require('node:fs');
  for (const state of ['missing', 'invalid-id', 'throw-on-ctrl', 'throw-on-file']) {
    const listeners = new Map();
    let prevented = 0, fallbacks = 0;
    const runtime = { id: state === 'missing' ? undefined : 'extension-id', sendMessage() { throw Error('Extension context invalidated.'); } };
    if (state === 'invalid-id') Object.defineProperty(runtime, 'id', { get() { throw Error('Extension context invalidated.'); } });
    const link = { matches: () => true, href: 'https://files.example.org/file.rar', click: () => { fallbacks++; } };
    const ctx = vm.createContext({ URL, Date, crypto: { randomUUID: () => 'gesture-123456789' }, chrome: { runtime }, document: {
      addEventListener: (name, fn) => { if (!listeners.has(name)) listeners.set(name, new Set()); listeners.get(name).add(fn); },
      removeEventListener: (name, fn) => listeners.get(name)?.delete(fn)
    } });
    vm.runInContext(fs.readFileSync(require.resolve('./src/click.js'), 'utf8'), ctx);
    const event = { type: 'click', button: 0, isTrusted: true, ctrlKey: state === 'throw-on-ctrl', composedPath: () => [link],
      preventDefault: () => { prevented++; }, stopImmediatePropagation() {} };
    for (const listener of [...listeners.get('click')]) assert.doesNotThrow(() => listener(event));
    assert.equal(listeners.get('click').size, 0);
    assert.equal(prevented, state === 'throw-on-file' ? 1 : 0);
    assert.equal(fallbacks, state === 'throw-on-file' ? 1 : 0);
  }
});
test('reinjecting the file handler replaces old window listeners before page navigation runs', async () => {
  const vm = require('node:vm'), fs = require('node:fs'), listeners = new Map(), messages = [];
  const ctx = vm.createContext({ URL, Date, crypto: { randomUUID: () => 'file-link-123456789' },
    document: { addEventListener() { assert.fail('file capture must precede document listeners'); } },
    window: {
      addEventListener(name, fn, capture) { assert.equal(capture, true); if (!listeners.has(name)) listeners.set(name, new Set()); listeners.get(name).add(fn); },
      removeEventListener(name, fn) { listeners.get(name)?.delete(fn); }
    }, chrome: { runtime: { id: 'extension-id', sendMessage: async message => { messages.push(message); return { ok: true }; } } }
  });
  const source = fs.readFileSync(require.resolve('./src/click.js'), 'utf8');
  vm.runInContext(source, ctx); vm.runInContext(source, ctx);
  assert.equal(listeners.get('click').size, 2);
  let blocked = false;
  const event = { type: 'click', isTrusted: true, button: 0, composedPath: () => [{ matches: () => true, href: 'https://files.example.org/file.rar?download=1' }],
    preventDefault() {}, stopImmediatePropagation() { blocked = true; } };
  for (const handler of listeners.get('click')) { handler(event); if (blocked) break; }
  assert(blocked); assert.equal(messages.length, 1); assert.equal(messages[0].action, 'file-link');
  await new Promise(done => setImmediate(done));
});
test('extension reload refreshes handlers on granted open tabs without touching private tabs', async () => {
  const vm = require('node:vm'), fs = require('node:fs'), injected = [], registrations = [], event = () => ({ addListener() {} });
  const chrome = {
    runtime: { onInstalled: event(), onStartup: event(), onMessage: event() },
    storage: { session: {}, local: {} }, downloads: { onCreated: event() },
    permissions: { contains: async () => true, getAll: async () => ({ origins: ['https://files.example.org/*'] }), onAdded: event(), onRemoved: event() },
    scripting: { getRegisteredContentScripts: async () => [{ id: 'fluxget-browser-click' }], updateContentScripts: async specs => registrations.push(...specs),
      executeScript: async spec => { injected.push(spec); if (spec.target.tabId === 2) throw Error('tab closed'); } },
    tabs: { onCreated: event(), query: async query => { assert.deepEqual(Array.from(query.url), ['https://files.example.org/*']); return [{ id: 1 }, { id: 2 }, { id: 3, incognito: true }]; } },
    contextMenus: { onClicked: event() }, alarms: { onAlarm: event() }
  };
  const ctx = vm.createContext({ URL, chrome, FluxGetCore: require('./src/core.js'), FLUXGET_BROWSER: 'chrome' });
  vm.runInContext(fs.readFileSync(require.resolve('./src/background.js'), 'utf8'), ctx);
  await vm.runInContext('syncClickScript()', ctx);
  assert.equal(registrations.length, 1);
  assert.deepEqual(injected.map(spec => spec.target.tabId), [1, 2]);
  assert(injected.every(spec => spec.target.allFrames && spec.files[0] === 'click.js'));
});
test('ordinary file links wait for FluxGet and only create browser navigation on explicit fallback', async () => {
  const vm = require('node:vm'), fs = require('node:fs');
  for (const decision of ['accepted', 'browser', 'canceled', 'lost']) {
    const listeners = {}, messages = [], notices = [];
    let resolve, reject, navigations = 0, prevented = 0;
    const result = new Promise((yes, no) => { resolve = yes; reject = no; });
    const link = { matches: () => true, href: item().url.replace('image.iso', 'file.rar'), click: () => { navigations++; } };
    const ctx = vm.createContext({ URL, Date, crypto: { randomUUID: () => 'file-link-123456789' },
      document: {
        addEventListener: (name, fn) => { (listeners[name] ||= []).push(fn); },
        createElement: () => ({ style: {}, setAttribute() {} }), body: { appendChild: note => notices.push(note.textContent) }
      },
      chrome: { runtime: { id: 'extension-id', sendMessage: message => { messages.push(message); return result; } } }
    });
    vm.runInContext(fs.readFileSync(require.resolve('./src/click.js'), 'utf8'), ctx);
    const event = { type: 'click', isTrusted: true, button: 0, composedPath: () => [link], preventDefault: () => { prevented++; }, stopImmediatePropagation() {} };
    const emit = e => listeners.click.forEach(fn => fn(e));
    emit({ ...event, isTrusted: false });
    emit({ ...event, defaultPrevented: true });
    emit(event); emit(event);
    assert.equal(messages.length, 1);
    assert.equal(messages[0].action, 'file-link');
    assert.equal(navigations, 0);
    assert.equal(prevented, 2);
    if (decision === 'lost') reject(Error('port disconnected'));
    else resolve({ ok: decision !== 'browser', canceled: decision === 'canceled' });
    await new Promise(done => setImmediate(done));
    assert.equal(navigations, decision === 'browser' ? 1 : 0);
    assert.equal(notices.length, decision === 'lost' ? 1 : 0);
  }
});
test('file-link handoff waits for approval and uses no Chrome download item', async () => {
  const vm = require('node:vm'), fs = require('node:fs');
  for (const decision of ['approve', 'browser', 'close', 'conflict']) {
    const approve = decision === 'approve';
    const event = () => ({ addListener() {} });
    const stored = { consent: true, profile: 'profile-1234567890' }, calls = [];
    const session = {};
    let onMessage;
    let answer;
    const approval = new Promise(resolve => { answer = resolve; });
    const chrome = {
      storage: { session: { get: async () => ({ ...session }), set: async values => Object.assign(session, values) }, local: {
        get: async () => ({ ...stored }), set: async values => Object.assign(stored, values),
        remove: async key => { delete stored[key]; }
      } },
      runtime: { id: 'extension-id', onInstalled: event(), onStartup: event(), onMessage: { addListener: listener => { onMessage = listener; } }, sendNativeMessage: async (_, request) => {
        calls.push(request.operation);
        if (request.operation === 'prepare') return { version: 1, ...(await approval) };
        if (request.operation === 'policy') return { version: 1, ok: true, enabled: true, mode: 'ask' };
        return { version: 1, ok: true, stage: 'active' };
      } },
      permissions: { contains: async () => true, onAdded: event(), onRemoved: event() },
      downloads: { onCreated: event() }, contextMenus: { onClicked: event() }, alarms: { onAlarm: event() }
    };
    const ctx = vm.createContext({ URL, chrome, FluxGetCore: require('./src/core.js'), FLUXGET_BROWSER: 'chrome' });
    vm.runInContext(fs.readFileSync(require.resolve('./src/background.js'), 'utf8'), ctx);
    const pending = new Promise(resolve => {
      assert.equal(onMessage({ action: 'file-link', url: 'https://files.example.org/file.rar', id: 'file-link-123456789', at: Date.now() },
        { id: 'extension-id', url: 'https://files.example.org/page', tab: { id: 1 } }, resolve), true);
    });
    await new Promise(done => setImmediate(done));
    assert.deepEqual(calls, ['policy', 'prepare']);
    answer({ ok: approve, stage: ['close', 'conflict'].includes(decision) ? 'canceled' : 'pending',
      error: decision === 'conflict' ? 'This filename is already used by another download' : undefined });
    const result = await pending;
    assert.equal(result.ok, decision !== 'browser');
    assert.equal(Boolean(result.canceled), ['close', 'conflict'].includes(decision));
    if (decision === 'conflict') assert.match(stored.lastMessage, /filename is already used/);
    assert.deepEqual(calls, approve ? ['policy', 'prepare', 'activate'] : ['policy', 'prepare', 'abort']);
    assert.equal((session.browserBypass || []).length, decision === 'browser' ? 1 : 0);
    assert(!Object.keys(stored).some(key => key.startsWith('handoff:')));
  }
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
test('capture reports why native policy or site permission leaves a download in the browser', async () => {
  for (const [policy, permission, reason] of [
    [{ ok: false, error: 'Open FluxGet' }, true, /Open FluxGet/],
    [{ ok: true, enabled: false }, true, /Enable integration/],
    [{ ok: true, enabled: true, mode: 'manual' }, true, /Manual sending only/],
    [{ ok: true, enabled: true, mode: 'ask' }, false, /site lacks permission/]
  ]) {
    const messages = [];
    const f = fixture({ native: async () => policy, enabled: async () => permission, notice: async message => messages.push(message) });
    await f.flow.capture(f.browser, proof);
    assert(!f.actions.includes('pause'));
    assert.match(messages[0], reason);
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
test('fallback identifies the blocking header category without exposing values', () => {
  for (const [name, reason] of [
    ['Cookie', /browser cookies/],
    ['Authorization', /authorization header/],
    ['Proxy-Authorization', /authorization header/],
    ['Range', /byte range/]
  ]) {
    const e = new Evidence();
    const b = item();
    e.start({ requestId: 'file', url: b.url, method: 'GET' });
    e.headers({ requestId: 'file', requestHeaders: [{ name, value: 'private-value' }] });
    e.response({ requestId: 'file', url: b.url, statusCode: 200 });
    const result = e.inspect(b);
    assert.equal(result.proof, null);
    assert.match(result.reason, reason);
    assert(!JSON.stringify([...e.requests.values()]).includes('private-value'));
  }
});
test('a cookie-bearing public file is verified anonymously before capture', async () => {
  const e = new Evidence();
  const b = item();
  e.start({ requestId: 'file', url: b.url, method: 'GET' });
  e.headers({ requestId: 'file', requestHeaders: [{ name: 'Cookie', value: 'PRIVATE' }] });
  e.response({ requestId: 'file', url: b.finalUrl, statusCode: 200, responseHeaders: [
    { name: 'Content-Length', value: '1000' }, { name: 'Content-Type', value: 'application/octet-stream' }, { name: 'ETag', value: '"file-1"' }
  ] });
  const observed = e.inspect(b);
  assert.equal(observed.proof, null);
  assert(observed.anonymous);
  const anonymous = async (url, options) => {
    assert.equal(url, b.finalUrl);
    assert.equal(options.credentials, 'omit');
    assert.equal(options.method, 'HEAD');
    assert.equal(options.redirect, 'error');
    assert.equal(options.cache, 'no-store');
    assert.equal(options.headers, undefined);
    return { status: 200, url, headers: new Headers({ 'Content-Length': '1000', 'Content-Type': 'application/octet-stream', ETag: '"file-1"' }) };
  };
  const f = fixture({ verifyPublic: async (item, request) => {
    f.actions.push('anonymous-check');
    await verifyPublicDownload(item, request, anonymous);
  } });
  await f.flow.capture(b, observed.anonymous.proof, observed.request);
  assert(f.actions.indexOf('anonymous-check') < f.actions.indexOf('pause'));
  assert(f.actions.includes('activate'));
  assert(!JSON.stringify([...e.requests.values()]).includes('PRIVATE'));
  for (const response of [
    { status: 403 }, { status: 200, headers: new Headers({ 'Content-Length': '1000', 'Content-Type': 'text/html' }) },
    { status: 200, headers: new Headers({ 'Content-Length': '1000', 'Content-Type': 'application/octet-stream', ETag: '"different"' }) },
    { status: 200, url: 'https://login.example.org/', headers: new Headers({}) }
  ]) {
    const rejected = fixture({ verifyPublic: (item, request) => verifyPublicDownload(item, request, async () => ({ url: b.finalUrl, headers: new Headers({}), ...response })) });
    await rejected.flow.capture(b, observed.anonymous.proof, observed.request);
    assert(!rejected.actions.includes('pause'));
    assert(!rejected.actions.includes('cancel'));
  }
  e.headers({ requestId: 'file', requestHeaders: [{ name: 'Authorization', value: 'SECRET' }] });
  assert.equal(e.inspect(b).anonymous, undefined);
});
test('a new download can match after an earlier request for its URL has finished', () => {
  let now = 1000;
  const e = new Evidence(() => now);
  const b = item();
  const observe = requestId => {
    e.start({ requestId, url: b.url, method: 'GET' });
    e.headers({ requestId, requestHeaders: [] });
    e.response({ requestId, url: b.finalUrl, statusCode: 200 });
  };
  observe('earlier');
  e.finish({ requestId: 'earlier' });
  now = 3000;
  observe('current');
  b.startTime = new Date(now).toISOString();
  const { request } = e.inspect(b);
  assert.equal(request.id, 'current');
  assert(e.claim(b, request));
  assert(!e.claim({ ...b, id: 2 }, request));
  observe('overlap');
  assert.match(e.inspect(b).reason, /Several matching/);
});
test('the public-file check uses HEAD without sending cookies or starting a body transfer', async () => {
  const http = require('node:http');
  const requests = [];
  const server = http.createServer((req, res) => {
    requests.push({ method: req.method, cookie: req.headers.cookie, authorization: req.headers.authorization });
    res.writeHead(200, { 'Content-Length': '1024', 'Content-Type': 'application/octet-stream', ETag: '"public-file"' });
    res.end();
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  try {
    const url = `http://127.0.0.1:${server.address().port}/file`;
    await verifyPublicDownload({ ...item(), url, finalUrl: url }, { status: 200, size: 1024, mime: 'application/octet-stream', etag: '"public-file"' });
    assert.deepEqual(requests, [{ method: 'HEAD', cookie: undefined, authorization: undefined }]);
  } finally {
    await new Promise(resolve => server.close(resolve));
  }
});

test('the download listener handles delayed browser safety checks and reports early completion', async () => {
  const vm = require('node:vm'), fs = require('node:fs');
  const event = () => ({ addListener() {} });
  const messages = [];
  const file = item();
  let checks = 0;
  let stopEarly = false;
  const chrome = {
    storage: {
      local: { get: async () => ({ consent: true }), set: async data => { if (data.lastMessage) messages.push(data.lastMessage); } },
      session: { get: async () => ({}), set: async () => {} }
    },
    runtime: { onInstalled: event(), onStartup: event(), onMessage: event() },
    permissions: { onAdded: event(), onRemoved: event() },
    downloads: { onCreated: event(), search: async () => [{ ...file, state: stopEarly ? 'complete' : 'in_progress', danger: ++checks >= 8 ? 'safe' : 'asyncScanning' }] },
    contextMenus: { onClicked: event() }, alarms: { onAlarm: event() }
  };
  const ctx = vm.createContext({ chrome, FluxGetCore: require('./src/core.js'), FLUXGET_BROWSER: 'chrome', file, setTimeout: fn => fn() });
  vm.runInContext(fs.readFileSync(require.resolve('./src/background.js'), 'utf8'), ctx);
  await vm.runInContext(`
    evidence.start({ requestId: 'file', url: file.url, method: 'GET' });
    evidence.headers({ requestId: 'file', requestHeaders: [] });
    evidence.response({ requestId: 'file', url: file.finalUrl, statusCode: 200 });
    handoff.capture = async () => chrome.storage.local.set({ lastMessage: 'capture attempted' });
    captureDownload(file);
  `, ctx);
  assert.equal(checks, 8);
  assert.deepEqual(messages, ['capture attempted']);
  stopEarly = true;
  await vm.runInContext('captureDownload(file)', ctx);
  assert.match(messages.at(-1), /finished or stopped before capture/);
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
test('Chrome pause and ownership remain under browser control after a Ctrl-click bypass', async () => {
  const vm = require('node:vm'), fs = require('node:fs');
  const event = () => ({ addListener() {} });
  const stored = {};
  let onChanged;
  const chrome = {
    storage: { session: { get: async () => ({ browserOwnedDownloads: [42] }) }, local: { get: async () => stored, set: async values => Object.assign(stored, values) } },
    runtime: { onInstalled: event(), onStartup: event(), onMessage: event() }, permissions: { onAdded: event(), onRemoved: event() },
    downloads: { onCreated: event(), onChanged: { addListener: listener => { onChanged = listener; } }, resume: () => assert.fail('Chrome pause was overridden') },
    contextMenus: { onClicked: event() }, alarms: { onAlarm: event() }
  };
  const ctx = vm.createContext({ chrome, FluxGetCore: require('./src/core.js'), FLUXGET_BROWSER: 'chrome' });
  vm.runInContext(fs.readFileSync(require.resolve('./src/background.js'), 'utf8'), ctx);
  onChanged({ id: 42, paused: { current: true, previous: false } });
  await vm.runInContext('diagnostics', ctx);
  assert.equal(stored.downloadDiagnostics.at(-1).event, 'chrome-state');
  assert.equal(stored.downloadDiagnostics.at(-1).detail.paused, true);
  await vm.runInContext('browserAction("resume", 42)', ctx);
  await vm.runInContext('diagnostics', ctx);
  assert.equal(stored.downloadDiagnostics.at(-1).event, 'browser-owned-action-blocked');
});
function popupFixture({ mode = 'ask', access = 'none', consent = true, approved = true, connected, integration = true, connectionError } = {}) {
  const vm = require('node:vm'), fs = require('node:fs'), elements = new Map(), calls = [];
  let onStorageChange, lastMessage;
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
    const remote = connectionError ? { ok: false, error: connectionError } : { ok: true, enabled: integration && (connected ?? consent), connected, mode };
    return message.action === 'status' ? { consent, remote, pending: 0, lastMessage } : { ok: true };
  } }, permissions: {
    getAll: async () => ({ permissions: access === 'none' ? [] : ['webRequest', 'scripting'], origins: access === 'all' ? ['http://*/*', 'https://*/*'] : access === 'limited' ? ['https://files.example.org/*'] : [] }),
    contains: async value => { assert.deepEqual(JSON.parse(JSON.stringify(value)), { permissions: ['webRequest', 'scripting'], origins: ['http://*/*', 'https://*/*'] }); return access === 'all'; },
    request: async value => { calls.push('request'); assert.deepEqual(JSON.parse(JSON.stringify(value)), { permissions: ['webRequest', 'scripting'], origins: ['http://*/*', 'https://*/*'] }); if (approved) access = 'all'; return approved; },
    remove: async () => { calls.push('remove'); access = 'none'; return true; }
  }, storage: { onChanged: { addListener: listener => { onStorageChange = listener; } } } };
  const context = vm.createContext({ document, chrome });
  vm.runInContext(fs.readFileSync(require.resolve('./src/popup.js'), 'utf8'), context);
  return {
    elements, calls, ready: () => vm.runInContext('refresh()', context),
    report: async message => {
      lastMessage = message;
      onStorageChange({ lastMessage: { newValue: message } }, 'local');
      await new Promise(resolve => setImmediate(resolve));
    }
  };
}
test('Test connection reports progress and the result beside the button', async () => {
  for (const [connectionError, expected] of [[undefined, /Connection OK/], ['Open FluxGet', /Connection failed\. Open FluxGet/]]) {
    const f = popupFixture({ connectionError }); await f.ready();
    const button = f.elements.get('test');
    const done = button.onclick();
    const result = f.elements.get('test-result');
    assert.equal(button.disabled, true);
    assert.equal(result.hidden, false);
    assert.match(result.textContent, /Testing connection/);
    await done;
    assert.match(result.textContent, expected);
    assert.equal(button.disabled, false);
  }
});
test('popup updates the reason for a download while open', async () => {
  const f = popupFixture(); await f.ready();
  await f.report('Kept in browser: The download finished before capture.');
  assert.match(f.elements.get('message').textContent, /finished before capture/);
});
test('popup reflects desktop disconnection and lets the user reconnect', async () => {
  const f = popupFixture({ mode: 'manual', consent: true, connected: false }); await f.ready();
  assert.equal(f.elements.get('enabled').checked, false);
  assert.match(f.elements.get('status').textContent, /connect to FluxGet/);
  f.elements.get('enabled').checked = true; await f.elements.get('enabled').change();
  assert.equal(f.elements.get('enabled').checked, true);
  const paused = popupFixture({ connected: true, integration: false }); await paused.ready();
  assert.equal(paused.elements.get('enabled').checked, true);
  assert.match(paused.elements.get('status').textContent, /Profile connected/);
  const localDisabled = popupFixture({ consent: false, connected: true }); await localDisabled.ready();
  assert.equal(localDisabled.elements.get('enabled').checked, false);
  assert.match(localDisabled.elements.get('status').textContent, /connect to FluxGet/);
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
