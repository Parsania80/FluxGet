/* Classic scripts support Chromium service workers and Firefox background scripts. */
if (typeof importScripts === "function") importScripts("config.js", "core.js");
const api = globalThis.browser || globalThis.chrome;
const core = globalThis.FluxGetCore;
const evidence = new core.Evidence();
const locks = new Set();
let diagnostics = Promise.resolve();
function traceDownload(event, id, detail) {
  diagnostics = diagnostics.catch(() => {}).then(async () => {
    const stored = await api.storage.local.get("downloadDiagnostics");
    const entries = Array.isArray(stored.downloadDiagnostics) ? stored.downloadDiagnostics : [];
    entries.push({ at: Date.now(), event, id, detail });
    await api.storage.local.set({ downloadDiagnostics: entries.slice(-40) });
  });
  diagnostics.catch(() => {});
}
async function browserAction(action, id) {
  const owned = await api.storage.session.get("browserOwnedDownloads");
  if ((owned.browserOwnedDownloads || []).includes(id)) {
    traceDownload("browser-owned-action-blocked", id, { action });
    if (action === "resume" || action === "erase") return;
    throw Error("Download belongs to the browser");
  }
  traceDownload("extension-" + action, id);
  try {
    const result = await api.downloads[action](action === "erase" ? { id, state: "interrupted", error: "USER_CANCELED" } : id);
    traceDownload("extension-" + action + "-done", id);
    return result;
  } catch (error) {
    traceDownload("extension-" + action + "-failed", id);
    throw error;
  }
}
api.downloads.onChanged?.addListener(change => {
  if (change.paused || change.state || change.error) {
    traceDownload("chrome-state", change.id, { paused: change.paused?.current, state: change.state?.current, error: change.error?.current });
  }
});
let clickScripts = Promise.resolve(), bypassWork = Promise.resolve();
function bypassTransaction(action) {
  const operation = bypassWork.then(async () => {
    const stored = await api.storage.session.get(["browserBypass", "browserOpeners"]);
    const bypass = new core.BrowserBypass(stored.browserBypass);
    const openers = (stored.browserOpeners || []).filter(t => Date.now() - t.at < 30000).slice(-64);
    const result = action(bypass, openers);
    await api.storage.session.set({ browserBypass: bypass.tickets, browserOpeners: openers.slice(-64) });
    return result;
  });
  bypassWork = operation.catch(() => {}); return operation;
}
function syncClickScript() {
  const operation = clickScripts.then(async () => {
    if (!api.scripting || !(await api.permissions.contains({ permissions: ["scripting"] }))) return;
    const grants = await api.permissions.getAll();
    const matches = (grants.origins || []).filter(origin => /^https?:\/\//.test(origin) || origin === "<all_urls>");
    const id = "fluxget-browser-click";
    const existing = await api.scripting.getRegisteredContentScripts({ ids: [id] });
    if (!matches.length) { if (existing.length) await api.scripting.unregisterContentScripts({ ids: [id] }); return; }
    const spec = { id, matches, js: ["click.js"], runAt: "document_start", allFrames: true, persistAcrossSessions: true };
    if (existing.length) await api.scripting.updateContentScripts([spec]);
    else await api.scripting.registerContentScripts([spec]);
    // Registration updates future documents. Refresh existing documents too:
    // their old extension world becomes invalid when an unpacked build reloads.
    const tabs = await api.tabs.query({ url: matches });
    await Promise.allSettled(tabs.filter(tab => !tab.incognito && Number.isInteger(tab.id)).map(tab =>
      api.scripting.executeScript({ target: { tabId: tab.id, allFrames: true }, files: ["click.js"] })));
  });
  clickScripts = operation.catch(() => {}); return operation;
}
async function takeBrowserBypass(item) {
  const traces = evidence.matches(item);
  const matching = traces.filter(r => r.urls.includes(item.url) && r.final === (item.finalUrl || item.url));
  if (matching.length !== 1) return false;
  const tab = matching[0].tab, tabs = [tab];
  if (Number.isInteger(tab) && tab >= 0) {
    try { const info = await api.tabs.get(tab); if (Number.isInteger(info.openerTabId)) tabs.push(info.openerTabId); } catch { /* A closed tab has no usable opener association. */ }
  }
  return bypassTransaction((bypass, openers) => {
    const fresh = evidence.matches(item);
    if (fresh.length !== 1 || fresh[0] !== matching[0]) return false;
    const opener = openers.find(t => t.tab === tab);
    if (opener) tabs.push(opener.parent);
    return bypass.take(item, fresh, tabs) && evidence.claim(item, matching[0]);
  });
}
api.tabs?.onCreated.addListener(tab => {
  if (Number.isInteger(tab.openerTabId)) bypassTransaction((bypass, openers) => { openers.push({ tab: tab.id, parent: tab.openerTabId, at: Date.now() }); }).catch(() => {});
});
let profilePromise;
function identity() {
  if (!profilePromise) profilePromise = (async () => {
    const data = await api.storage.local.get("profile");
    if (data.profile) return data.profile;
    const profile = crypto.randomUUID(); await api.storage.local.set({ profile }); return profile;
  })().catch(error => { profilePromise = undefined; throw error; });
  return profilePromise;
}
async function native(message) {
  const response = await api.runtime.sendNativeMessage(core.HOST, { version: 1, browser: FLUXGET_BROWSER, profile: await identity(), ...message });
  if (!response || response.version !== 1) throw Error("The FluxGet bridge needs an update");
  return response;
}
async function localCaptureBlock() {
  const s = await api.storage.local.get(["consent", "pauseUntil"]);
  if (s.consent !== true) return "Enable this browser profile in the FluxGet extension.";
  if (s.pauseUntil > Date.now()) return "Capture is temporarily paused.";
  return "";
}
async function localEnabled() { return !(await localCaptureBlock()); }
async function scopeAllowed(item) {
  if (!item) return true;
  const origins = [...new Set([item.url, item.finalUrl || item.url].map(raw => { const u = new URL(raw); return u.protocol + "//" + u.hostname + "/*"; }))];
  return api.permissions.contains({ permissions: ["webRequest"], origins });
}
async function records() { const s = await api.storage.local.get(null); return Object.entries(s).filter(([k]) => k.startsWith("handoff:")).map(([, v]) => v); }
const handoff = new core.Handoff({ native, verifyPublic: core.verifyPublicDownload, enabled: async item => (await localEnabled()) && (await scopeAllowed(item)), uuid: () => crypto.randomUUID(), pause: id => browserAction("pause", id), cancel: id => browserAction("cancel", id), resume: id => browserAction("resume", id), erase: id => browserAction("erase", id), item: async id => (await api.downloads.search({ id }))[0], save: r => api.storage.local.set({ ["handoff:" + r.request_id]: r }), remove: id => api.storage.local.remove("handoff:" + id), notice: text => api.storage.local.set({ lastMessage: text }) });
function observe() {
  if (!api.webRequest) return;
  const filter = { urls: ["http://*/*", "https://*/*"] };
  if (!api.webRequest.onBeforeRequest.hasListener(before)) api.webRequest.onBeforeRequest.addListener(before, filter);
  if (!api.webRequest.onBeforeSendHeaders.hasListener(headers)) api.webRequest.onBeforeSendHeaders.addListener(headers, filter, FLUXGET_BROWSER === "firefox" ? ["requestHeaders"] : ["requestHeaders", "extraHeaders"]);
  if (!api.webRequest.onHeadersReceived.hasListener(response)) api.webRequest.onHeadersReceived.addListener(response, filter, ["responseHeaders"]);
  if (!api.webRequest.onBeforeRedirect.hasListener(redirect)) api.webRequest.onBeforeRedirect.addListener(redirect, filter);
  if (!api.webRequest.onCompleted.hasListener(finish)) api.webRequest.onCompleted.addListener(finish, filter);
  if (!api.webRequest.onErrorOccurred.hasListener(finish)) api.webRequest.onErrorOccurred.addListener(finish, filter);
}
function before(d) { evidence.start(d); }
function headers(d) { evidence.headers(d); }
function response(d) { evidence.response(d); }
function redirect(d) { evidence.redirect(d); }
function finish(d) { evidence.finish(d); }
// Register synchronously so service-worker wakeup does not lose the request.
try { observe(); } catch { /* Site observation is optional until permission is granted. */ }
api.permissions.onRemoved.addListener(() => { evidence.requests.clear(); bypassTransaction(b => { b.tickets = []; }).catch(() => {}); syncClickScript().catch(() => {}); });
api.permissions.onAdded.addListener(() => { try { observe(); } catch { /* No capture without evidence. */ } syncClickScript().catch(() => {}); });
async function captureDownload(item) {
  traceDownload("chrome-created", item.id, { paused: item.paused, state: item.state });
  const owned = await api.storage.session.get("browserOwnedDownloads");
  if ((owned.browserOwnedDownloads || []).includes(item.id)) return;
  if (locks.has(item.id)) return;
  const kept = reason => api.storage.local.set({ lastMessage: "Kept in browser: " + reason });
  if (locks.size >= 4) { await kept("Too many downloads are being checked at once."); return; }
  // onCreated may precede the final filename/danger update. Wait briefly, then
  // use only a current, still-active, explicitly safe item and matching evidence.
  locks.add(item.id);
  try {
    const blocked = await localCaptureBlock();
    if (blocked) { await kept(blocked); return; }
    if ((await records()).length >= 32) { await kept("Use Retry pending handoffs in the extension to clear pending downloads."); return; }
    // Allow the trusted page gesture message to arrive before deciding capture.
    await new Promise(resolve => setTimeout(resolve, 50));
    let reason;
    for (let n = 0; n < 16; n++) {
      const current = (await api.downloads.search({ id: item.id }))[0];
      if (!current || current.state !== "in_progress" || current.paused || current.incognito !== false || current.byExtensionId) {
        reason = evidence.inspect(current).reason;
        break;
      }
      if (await takeBrowserBypass(current)) {
        const stored = await api.storage.session.get("browserOwnedDownloads");
        const owned = stored.browserOwnedDownloads || [];
        await api.storage.session.set({ browserOwnedDownloads: [...new Set([...owned, current.id])].slice(-128) });
        traceDownload("browser-bypass", current.id);
        await kept("Ctrl + left-click.");
        return;
      }
      const observed = evidence.inspect(current);
      reason = observed.reason;
      const proof = observed.proof || observed.anonymous?.proof;
      const { request } = observed;
      if (proof && evidence.claim(current, request)) {
        await handoff.capture(current, proof, observed.anonymous ? request : undefined);
        return;
      }
      if (n < 15) await new Promise(resolve => setTimeout(resolve, 200));
    }
    await kept(reason || "The download changed before FluxGet could match it.");
  } catch { await kept("FluxGet could not check this download."); }
  finally { locks.delete(item.id); }
}
api.downloads.onCreated.addListener(item => { captureDownload(item).catch(() => {}); });
api.runtime.onInstalled.addListener(async () => {
  await identity();
  await syncClickScript();
  await api.contextMenus.removeAll();
  api.contextMenus.create({ id: "fluxget-send-link", title: "Send link to FluxGet", contexts: ["link"] });
  api.alarms.create("fluxget-recover", { periodInMinutes: 1 });
});
async function sendLink(raw, request_id) {
  const record = { request_id, item: "link:" + request_id, kind: "link", stage: "preparing", created: Date.now() };
  if (handoff.active.has(record.item)) return { ok: true, pending: true };
  if (handoff.active.size >= 4) return { ok: false };
  handoff.active.add(record.item);
  try {
    await api.storage.local.set({ ["handoff:" + request_id]: record });
    const prepared = await native({ operation: "prepare", request_id, kind: "link", url: raw, filename: core.filename({ url: raw }) });
    if (!prepared.ok && prepared.stage === "canceled") {
      record.stage = "dismissed";
      record.notice = core.diagnostic(prepared.error || "Download canceled in FluxGet");
      await api.storage.local.set({ ["handoff:" + request_id]: record });
      await handoff.reconcile(record);
      return { ok: true, canceled: true };
    }
    if (!prepared.ok) throw Error(prepared.error || "FluxGet declined the link");
    record.stage = "link-activating";
    await api.storage.local.set({ ["handoff:" + request_id]: record });
    await handoff.reconcile(record);
    await api.storage.local.set({ lastMessage: "Link sent to FluxGet" });
    return { ok: true };
  } catch (error) {
    const message = core.diagnostic(error.message || "Could not send this link");
    if (record.stage === "dismissed") {
      await api.storage.local.set({ lastMessage: "Download canceled. Browser fallback is disabled; cleanup will retry." }).catch(() => {});
      return { ok: true, canceled: true, pending: true };
    }
    await api.storage.local.set({ lastMessage: record.stage === "link-activating" ? "Link accepted. Use Retry pending handoffs to finish recovery." : message }).catch(() => {});
    if (record.stage === "link-activating") return { ok: true, pending: true };
    try { await native({ operation: "abort", request_id, kind: "link" }); } catch { /* Unconfirmed desktop jobs stay paused. */ }
    await api.storage.local.remove("handoff:" + request_id).catch(() => {});
    return { ok: false, error: message };
  } finally {
    handoff.active.delete(record.item);
  }
}
api.contextMenus.onClicked.addListener(async (info, tab) => {
  if (info.menuItemId !== "fluxget-send-link" || tab?.incognito || !(await localEnabled())) return;
  try {
    const raw = info.linkUrl;
    if (!core.publicURL(raw)) throw Error("This link must stay in the browser");
    const policy = await native({ operation: "policy" });
    if (!policy.enabled) throw Error("Enable this browser and profile in FluxGet first");
    await sendLink(raw, crypto.randomUUID());
  } catch (error) { await api.storage.local.set({ lastMessage: core.diagnostic(error.message || "Could not send this link") }); }
});
api.alarms.onAlarm.addListener(async alarm => { if (alarm.name === "fluxget-recover") await handoff.recover(await records()); });
api.runtime.onStartup.addListener(async () => { await syncClickScript(); await handoff.recover(await records()); });
api.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (message.action === "browser-click" || message.action === "file-link") {
    let origin;
    try { const page = new URL(sender.url); if (!/^https?:$/.test(page.protocol)) return false; origin = page.origin + "/*"; } catch { return false; }
    if (sender.id !== api.runtime.id || !Number.isInteger(sender.tab?.id) || sender.incognito || sender.tab.incognito || typeof message.id !== "string" || message.id.length > 80 || typeof message.url !== "string" || (message.url && !core.publicURL(message.url)) || !Number.isFinite(message.at) || Math.abs(Date.now() - message.at) > 5000) return false;
    (async () => {
      if (!(await localEnabled()) || !(await api.permissions.contains({ permissions: ["scripting"], origins: [origin] }))) return { ok: false };
      if (message.action === "file-link") {
        if (!core.publicURL(message.url) || !/^[a-zA-Z0-9_-]{16,80}$/.test(message.id)) return { ok: false };
        traceDownload("file-link", sender.tab.id);
        const policy = await native({ operation: "policy" });
        if (!policy.ok || !policy.enabled || policy.mode === "manual" || (await records()).length >= 32) return { ok: false };
        const result = await sendLink(message.url, message.id);
        if (!result.ok) {
          await bypassTransaction(b => b.add({ id: message.id, tab: sender.tab.id, url: message.url, at: Date.now() }));
        }
        return result;
      }
      traceDownload("ctrl-gesture", sender.tab.id, { hasURL: Boolean(message.url), age: Date.now() - message.at });
      await bypassTransaction(b => b.add({ id: message.id, tab: sender.tab.id, url: message.url, at: message.at }));
      return { ok: true };
    })().then(sendResponse, () => sendResponse({ ok: false }));
    return true;
  }
  if (sender.id !== api.runtime.id || !sender.url?.startsWith(api.runtime.getURL(""))) return false;
  (async () => {
    if (message.action === "status") {
      const local = await api.storage.local.get(["consent", "lastMessage", "pauseUntil"]);
      let remote;
      try {
        remote = await native({ operation: "profile-status" });
        if (!remote.ok && remote.error === "unknown browser operation") remote = await native({ operation: "policy" });
      } catch { remote = { ok: false, error: "Open FluxGet and test the native connection" }; }
      return { ...local, remote, pending: (await records()).length };
    }
    if (message.action === "profile") {
      if (message.consent !== true) await api.storage.local.set({ consent: false });
      const response = await native({ operation: "profile", consent: message.consent === true });
      if (!response.ok) throw Error(response.error);
      await api.storage.local.set({ consent: message.consent === true }); return response;
    }
    if (message.action === "pause") { await api.storage.local.set({ pauseUntil: Date.now() + 15 * 60 * 1000 }); return { ok: true }; }
    if (message.action === "retry") { await handoff.recover(await records()); return { ok: true }; }
    if (message.action === "permission") { observe(); await syncClickScript(); return { ok: true }; }
    throw Error("Unknown extension action");
  })().then(sendResponse, error => sendResponse({ ok: false, error: error.message || "Connection failed" }));
  return true;
});
