/* Isolated content script: observe trusted Ctrl+left clicks, without changing navigation. */
(() => {
  const api = globalThis.browser || globalThis.chrome;
  let pointer;
  function send(event) {
    if (event.type === "pointerdown") pointer = undefined;
    if (!event.isTrusted || event.button !== 0 || !event.ctrlKey) return;
    const link = event.composedPath().find(node => node?.matches?.("a[href],area[href]"));
    let url = "";
    if (link) {
      try { const value = new URL(link.href); if (!/^https?:$/.test(value.protocol)) return; value.hash = ""; url = value.href; } catch { return; }
    }
    const id = event.type === "click" && pointer ? pointer.id : crypto.randomUUID();
    pointer = event.type === "pointerdown" ? { id } : undefined;
    api.runtime.sendMessage({ action: "browser-click", id, url, at: Date.now() }).catch(() => {});
  }
  document.addEventListener("pointerdown", send, true);
  document.addEventListener("click", send, true);
})();
