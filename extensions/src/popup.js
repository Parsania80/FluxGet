const api = globalThis.browser || globalThis.chrome;
const $ = id => document.getElementById(id);
const CAPTURE_ACCESS = { permissions: ["webRequest", "scripting"], origins: ["http://*/*", "https://*/*"] };
let captureMode;
async function command(action, data = {}) { const result = await api.runtime.sendMessage({ action, ...data }); if (result?.ok === false) throw Error(result.error || "Could not apply this change"); return result; }
async function allowAllWebsites() {
  // Request directly from the click/change handler, before unrelated async work.
  if (!(await api.permissions.request(CAPTURE_ACCESS))) throw Error("Website access was not granted. Downloads stay in your browser.");
  await command("permission");
}
async function refresh() {
  try {
    const result = await command("status"); $("enabled").checked = result.consent === true;
    captureMode = result.remote.mode;
    $("status").textContent = !result.remote.ok ? result.remote.error : result.remote.enabled ? `Connected · ${result.remote.mode === "automatic" ? "Automatic capture" : result.remote.mode === "manual" ? "Manual sending" : "Ask first"}` : "Connected · Disabled in FluxGet or this profile";
    const grants = await api.permissions.getAll();
    const hasSites = grants.permissions?.includes("webRequest") && grants.origins?.some(origin => /^https?:\/\//.test(origin) || origin === "<all_urls>");
    const allSites = await api.permissions.contains(CAPTURE_ACCESS);
    $("scope").textContent = allSites ? "All websites allowed. New sites do not need separate approval. Ctrl + left-click keeps a download in your browser." : hasSites ? "Finish setup to enable all websites and the Ctrl + left-click shortcut." : "Website access is not enabled. Approve it once to capture downloads across websites.";
    $("grant").hidden = allSites;
    $("revoke").hidden = !hasSites;
    const needsSites = result.remote.ok && result.remote.enabled && result.remote.mode !== "manual" && !allSites;
    $("setup").hidden = !needsSites;
    if (needsSites) {
      $("status").textContent += hasSites ? " · Capture setup incomplete" : " · Site access required";
      $("setup").textContent = "Choose Enable capture for all websites below and approve the browser permission once. Then reload the page and retry. No need to enter individual websites.";
    }
    $("message").textContent = `${result.pending || 0} pending handoffs. ${result.lastMessage || ""}${result.pauseUntil > Date.now() ? " Capture is temporarily paused." : ""}`;
  } catch (error) { $("status").textContent = error.message; }
}
$("enabled").addEventListener("change", async () => {
  let error;
  try { const consent = $("enabled").checked; if (consent && captureMode !== "manual") await allowAllWebsites(); await command("profile", { consent }); } catch (e) { error = e.message; }
  await refresh();
  if (error) $("message").textContent = error;
});
$("test").onclick = refresh;
$("pause").onclick = async () => { await command("pause"); await refresh(); };
$("retry").onclick = async () => { await command("retry"); await refresh(); };
$("grant").onclick = async () => {
  try { await allowAllWebsites(); await refresh(); $("message").textContent = "All websites allowed. Reload the page and retry your download."; } catch (e) { $("message").textContent = e.message; }
};
$("revoke").onclick = async () => {
  try {
    const grants = await api.permissions.getAll();
    const origins = (grants.origins || []).filter(origin => /^https?:\/\//.test(origin) || origin === "<all_urls>");
    if (!(await api.permissions.remove({ origins, permissions: ["webRequest", "scripting"] }))) throw Error("Website access could not be removed");
    await refresh(); $("message").textContent = "Website access removed. New downloads stay in the browser; manual sending is still available.";
  } catch (e) { $("message").textContent = e.message; }
};
refresh();
