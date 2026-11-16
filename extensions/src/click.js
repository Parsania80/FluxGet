/* Isolated content script for file-link capture and the Ctrl+left-click bypass. */
(() => {
  const api = globalThis.browser || globalThis.chrome;
  const target = globalThis.window || document;
  globalThis.fluxGetClickCleanup?.();
  let pointer;
  let bypassClick = false;
  let sending = false;
  function cleanup() {
    target.removeEventListener("pointerdown", send, true);
    target.removeEventListener("click", send, true);
    target.removeEventListener("click", capture, true);
  }
  globalThis.fluxGetClickCleanup = cleanup;
  function connected() {
    try {
      if (api.runtime.id) return true;
    } catch { /* Chrome invalidates content scripts after extension reload. */ }
    cleanup();
    return false;
  }
  const filePath = /\.(?:zip|rar|7z|tar|gz|bz2|xz|iso|exe|msi|msix|deb|rpm|dmg|pkg|apk|pdf|epub|mp3|m4a|flac|wav|mp4|mkv|webm)(?:$)/i;
  function capture(event) {
    if (!event.isTrusted || event.defaultPrevented || event.button !== 0 || event.ctrlKey || bypassClick || event.shiftKey || event.altKey || event.metaKey || !connected()) return;
    const link = event.composedPath().find(node => node?.matches?.("a[href],area[href]"));
    if (!link) return;
    let url;
    try {
      url = new URL(link.href);
      if (!/^https?:$/.test(url.protocol) || url.username || url.password || url.hash || !filePath.test(url.pathname)) return;
    } catch { return; }
    event.preventDefault();
    event.stopImmediatePropagation();
    if (sending) return;
    sending = true;
    let reply;
    try {
      reply = api.runtime.sendMessage({ action: "file-link", id: crypto.randomUUID(), url: url.href, at: Date.now() });
    } catch {
      cleanup();
      sending = false;
      link.click();
      return;
    }
    reply.then(result => {
      if (result?.ok === false) link.click();
    }).catch(() => {
      // A lost reply may follow admission. Keep the browser from creating a
      // duplicate and tell the user where to check the result.
      const note = document.createElement("div");
      note.textContent = "FluxGet connection interrupted. Check FluxGet before retrying this download.";
      note.setAttribute("role", "alert");
      Object.assign(note.style, { position: "fixed", bottom: "16px", right: "16px", zIndex: "2147483647", padding: "16px", background: "#162b29", color: "white", maxWidth: "360px" });
      document.body.appendChild(note);
    }).finally(() => { sending = false; });
  }
  function send(event) {
    if (!event.isTrusted || event.button !== 0 || !connected()) return;
    if (event.type === "pointerdown") pointer = undefined;
    const gesture = event.type === "click" ? pointer : undefined;
    if (event.type === "click") bypassClick = Boolean(event.ctrlKey || gesture);
    if (!event.ctrlKey && !gesture) return;
    const link = event.composedPath().find(node => node?.matches?.("a[href],area[href]"));
    let url = "";
    if (link) {
      try { const value = new URL(link.href); if (!/^https?:$/.test(value.protocol)) return; value.hash = ""; url = value.href; } catch { return; }
    }
    const id = gesture?.id || crypto.randomUUID();
    const at = gesture?.at ?? Date.now();
    pointer = event.type === "pointerdown" ? { id, at } : undefined;
    try {
      api.runtime.sendMessage({ action: "browser-click", id, url, at }).catch(() => {});
    } catch { cleanup(); }
  }
  target.addEventListener("pointerdown", send, true);
  target.addEventListener("click", send, true);
  target.addEventListener("click", capture, true);
})();
