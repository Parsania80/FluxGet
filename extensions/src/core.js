/* Shared policy and handoff logic. Browser APIs are injected for deterministic tests. */
(function (root) {
  "use strict";
  const HOST = "io.github.parsania80.fluxget";
  function diagnostic(message) { return String(message).replace(/https?:\/\/[^\s"'<>]+/g, "[download URL]"); }
  function publicURL(raw) {
    try { const u = new URL(raw); return raw.length <= 8192 && !/[\r\n\x00]/.test(raw) && /^https?:$/.test(u.protocol) && !u.username && !u.password && !u.hash; } catch { return false; }
  }
  function filename(item) {
    const name = (item.filename || "").split(/[\\/]/).pop();
    if (name && name !== "." && name !== ".." && name.length <= 240 && !/[\x00-\x1f\x7f]/.test(name)) return name;
    try { const value = decodeURIComponent(new URL(item.finalUrl || item.url).pathname.split("/").pop()); return value && !/[\\/\x00-\x1f\x7f]/.test(value) ? value : "download"; } catch { return "download"; }
  }
  class Evidence {
    constructor(now = () => Date.now()) { this.now = now; this.requests = new Map(); }
    start(d) {
      this.prune();
      const previous = this.requests.get(d.requestId);
      this.requests.set(d.requestId, { id: d.requestId, tab: d.tabId, started: previous?.started ?? this.now(), urls: [...(previous?.urls || []), d.url], method: d.method, unsafe: Boolean(previous?.unsafe) || d.method !== "GET" || !publicURL(d.url) || d.incognito === true, at: this.now(), headers: false, credentials: Boolean(previous?.credentials), status: 0 });
    }
    headers(d) {
      const r = this.requests.get(d.requestId); if (!r) return;
      r.headers = true;
      r.credentials ||= (d.requestHeaders || []).some(h => /^(cookie|authorization|proxy-authorization|range)$/i.test(h.name));
      // Retain only a boolean, never cookie/header contents.
    }
    response(d) { const r = this.requests.get(d.requestId); if (r) { r.status = d.statusCode; r.final = d.url; r.at = this.now(); } }
    redirect(d) { const r = this.requests.get(d.requestId); if (r && (!r.headers || !publicURL(d.redirectUrl))) r.unsafe = true; }
    prune() {
      for (const [id, r] of this.requests) if (this.now() - r.at > 30000) this.requests.delete(id);
      while (this.requests.size >= 512) this.requests.delete(this.requests.keys().next().value);
    }
    inspect(item) {
      this.prune();
      const reject = reason => ({ proof: null, reason });
      if (!item) return reject("The browser download is no longer available.");
      if (!publicURL(item.url) || !publicURL(item.finalUrl || item.url)) {
        return reject("This link is not a supported public HTTP/HTTPS file URL.");
      }
      if (item.incognito !== false) return reject("Private or unknown browser profiles are not supported.");
      if (item.danger !== "safe") return reject("The browser has not marked this download safe for capture.");
      if (item.state !== "in_progress") return reject("The browser download finished or stopped before capture.");
      if (item.paused) return reject("This browser download is already paused. Capture applies to new downloads.");
      if (item.byExtensionId) return reject("Another extension manages this download.");
      const matches = [...this.requests.values()].filter(r => r.urls.includes(item.url) || r.final === (item.finalUrl || item.url));
      if (!matches.length) return reject("The original network request was not observed. Reload the page and retry.");
      if (matches.length !== 1) return reject("Several matching network requests were observed; capture could not identify one safely.");
      const r = matches[0];
      if (!r.urls.includes(item.url) || r.final !== (item.finalUrl || item.url)) return reject("The complete original-to-final download request was not observed.");
      if (r.unsafe) return reject("The request method or redirect is not supported for capture.");
      if (!r.headers) return reject("Request-header checks are unavailable for this download.");
      if (r.credentials) return reject("The request uses cookies, authorization or range headers that this preview cannot replay.");
      if (![200, 206].includes(r.status)) return reject("The server did not return a supported download response.");
      if (this.now() - r.at > 15000) return reject("The observed download request is too old to capture safely.");
      return { proof: { method: "GET", headers_observed: true, credentials: false, safe: true, private: false }, reason: "" };
    }
    proof(item) { return this.inspect(item).proof; }
  }
  class BrowserBypass {
    constructor(tickets = [], now = () => Date.now()) { this.tickets = Array.isArray(tickets) ? tickets : []; this.now = now; this.prune(); }
    prune() { this.tickets = this.tickets.filter(t => t && Number.isInteger(t.tab) && t.tab >= 0 && typeof t.id === "string" && t.id.length <= 80 && typeof t.url === "string" && (!t.url || publicURL(t.url)) && Number.isFinite(t.at) && this.now() - t.at >= 0 && this.now() - t.at < (t.url ? 30000 : 5000)).slice(-32); }
    add(ticket) { this.prune(); if (!this.tickets.some(t => t.id === ticket.id && t.tab === ticket.tab)) this.tickets.push(ticket); this.prune(); }
    take(item, traces, tabs) {
      this.prune();
      const matches = traces.filter(r => r.urls.includes(item.url) && r.final === (item.finalUrl || item.url));
      if (matches.length !== 1) return false;
      const request = matches[0];
      const index = this.tickets.findIndex(t => !t.used && tabs.includes(t.tab) && request.started >= t.at && (!t.url || request.urls.includes(t.url)));
      if (index < 0) return false;
      this.tickets[index].used = true; return true;
    }
  }
  class Handoff {
    constructor(api) { this.api = api; this.active = new Set(); }
    async capture(item, proof) {
      if (!proof || this.active.has(item.id) || this.active.size >= 4) return;
      this.active.add(item.id);
      let record;
      try {
        const policy = await this.api.native({ operation: "policy" });
        if (!policy.enabled || policy.mode === "manual" || !(await this.api.enabled(item))) return;
        record = { item: item.id, request_id: this.api.uuid(), kind: "capture", stage: "pausing", created: Date.now() };
        await this.api.save(record);
        await this.api.pause(item.id);
        const paused = await this.api.item(item.id);
        if (!paused || paused.state !== "in_progress" || !paused.paused || paused.danger !== "safe" || paused.incognito !== false) throw Error("Browser download could not be paused safely");
        record.stage = "preparing"; await this.api.save(record);
        const accepted = await this.api.native({ operation: "prepare", request_id: record.request_id, kind: "capture", url: item.finalUrl || item.url, filename: filename(paused), proof });
        if (!accepted.ok || accepted.stage !== "pending") throw Error(accepted.error || "FluxGet did not accept the download");
        record.stage = "prepared"; await this.api.save(record);
        const fresh = await this.api.native({ operation: "policy" });
        const receipt = await this.api.native({ operation: "status", request_id: record.request_id, kind: record.kind });
        if (!receipt.ok || receipt.stage !== "pending") throw Error("FluxGet destination acceptance is no longer available");
        const latest = await this.api.item(item.id);
        if (!fresh.enabled || !(await this.api.enabled(item)) || !latest?.paused || latest.state !== "in_progress" || latest.danger !== "safe" || latest.incognito !== false) throw Error("Capture was disabled or the browser item changed");
        // Persist cancellation intent before calling the browser. Recovery must
        // inspect browser state, never guess that cancellation succeeded.
        record.stage = "canceling"; await this.api.save(record);
        await this.api.cancel(item.id);
        await this.reconcile(record);
      } catch (error) {
        if (record) {
          if (record.stage === "cleanup") { await this.api.notice("Download is in FluxGet; browser history cleanup is pending. Choose Retry pending handoffs."); return; }
          if (record.stage === "canceling" || record.stage === "activating") await this.api.notice("Handoff needs recovery. Open FluxGet's extension and choose Retry pending handoffs.");
          else await this.abandon(record);
        }
        await this.api.notice(diagnostic(error.message || "Kept in the browser"));
      } finally { this.active.delete(item.id); }
    }
    async abandon(record) {
      try { await this.api.native({ operation: "abort", request_id: record.request_id, kind: record.kind }); } catch { /* Unknown pending jobs stay paused. */ }
      const item = await this.api.item(record.item);
      if (item?.state === "in_progress" && item.paused) await this.api.resume(record.item);
      await this.api.remove(record.request_id);
    }
    async reconcile(record) {
      if (record.kind === "link") {
        if (record.stage !== "link-activating") { await this.api.native({ operation: "abort", request_id: record.request_id, kind: "link" }); await this.api.remove(record.request_id); return; }
        const reply = await this.api.native({ operation: "activate", request_id: record.request_id, kind: "link", canceled: true });
        if (!reply.ok || reply.stage !== "active") throw Error(reply.error || "Open FluxGet to recover the link");
        await this.api.remove(record.request_id); return;
      }
      if (record.stage === "cleanup") {
        const receipt = await this.api.native({ operation: "status", request_id: record.request_id, kind: record.kind });
        if (!receipt.ok || receipt.stage !== "active") throw Error("FluxGet ownership could not be confirmed for history cleanup");
        await this.cleanup(record); return;
      }
      const item = await this.api.item(record.item);
      if (item?.state === "interrupted" && item.error === "USER_CANCELED" && ["canceling", "activating"].includes(record.stage)) {
        record.stage = "activating"; await this.api.save(record);
        const response = await this.api.native({ operation: "activate", request_id: record.request_id, kind: record.kind, canceled: true });
        if (!response.ok || response.stage !== "active") throw Error(response.error || "Open FluxGet to recover this accepted download");
        // Persist confirmed ownership before erasing the exact canceled entry.
        // A crash after erase must recover cleanup without recreating a transfer.
        record.stage = "cleanup"; await this.api.save(record);
        await this.cleanup(record); return;
      }
      if (!item && ["canceling", "activating"].includes(record.stage)) throw Error("Browser state is unavailable; check FluxGet before retrying");
      await this.abandon(record);
    }
    async cleanup(record) {
      const item = await this.api.item(record.item);
      if (item?.state === "interrupted" && item.error === "USER_CANCELED") await this.api.erase(record.item);
      await this.api.remove(record.request_id);
      await this.api.notice("Download handed to FluxGet");
    }
    async recover(records) {
      for (const record of records) {
        if (this.active.has(record.item)) continue;
        this.active.add(record.item);
        try { await this.reconcile(record); } catch { await this.api.notice(record.stage === "cleanup" ? "Download is in FluxGet; browser history cleanup is pending. Choose Retry pending handoffs." : "A handoff needs recovery. Open FluxGet, then retry pending handoffs."); }
        finally { this.active.delete(record.item); }
      }
    }
  }
  const exported = { HOST, publicURL, filename, diagnostic, Evidence, BrowserBypass, Handoff };
  root.FluxGetCore = exported;
  if (typeof module !== "undefined") module.exports = exported;
})(globalThis);
