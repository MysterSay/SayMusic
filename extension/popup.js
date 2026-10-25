"use strict";
const tr = s => window.SayI18n ? window.SayI18n.t(s) : s;
const $ = (id) => document.getElementById(id);
async function send(message) {
  const r = await chrome.runtime.sendMessage(message);
  if (!r?.ok) throw Error(r?.error || tr("Немає відповіді"));
  return r;
}
async function sessionAction(type) {
 // Keep permissions.request directly in the click call chain, before messaging.
 const granted=await chrome.permissions.request({permissions:["cookies"]});
 if(!granted)throw Error(tr("Без тимчасового дозволу сесію не передано. Додавання посилань доступне без нього."));
 return send({type});
}
function action(id, fn) {
  $(id).onclick = async () => {
    $(id).disabled = true;
    $("message").textContent = tr("Зачекай…");
    try {
      const r = await fn();
      $("message").textContent = tr(r.message || "Готово");
      await status();
    } catch (e) {
      $("message").textContent = tr(e.message);
    } finally {
      $(id).disabled = false;
    }
  };
}
async function status() {
  try {
    const r = await send({ type: "STATUS" });
    window.SayI18n?.set(r.language || "uk");
    $("state").textContent = !r.running
      ? r.paired ? tr("Відкрий Say Music для локального з’єднання") : tr("Say Music закрито — додавання запустить програму")
      : tr("Програму підключено • YouTube ") + (r.authenticated ? "✓" : "—") + " • Spotify " + (r.spotifyConnected ? "✓" : "—");
  } catch (e) {
    $("state").textContent = tr(e.message);
  }
}
action("add", async () => {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  let info={url:tab?.url || ""};
  if(tab?.id && /https:\/\/(?:music\.|www\.)?youtube\.com/.test(tab.url || "")) {
    try { info={...info,...await chrome.tabs.sendMessage(tab.id,{type:"PAGE_INFO"})}; } catch {}
  }
  return send({ ...info, type: "ADD" });
});
action("authSpotify", () => sessionAction("AUTH_SPOTIFY"));
action("auth", () => sessionAction("AUTH"));
action("open", () => send({ type: "OPEN" }));
status();
$("copySession").onclick = async () => {
  $("copySession").disabled=true;
  try {
    const result=await sessionAction("EXPORT_SESSION");
    await navigator.clipboard.writeText(result.text);
    $("message").textContent=tr("Сесію скопійовано. Протягом 10 хвилин: Say Music → Налаштування → Вставити сесію. Після вставлення зв’язок працюватиме автоматично.");
    $("state").textContent=tr("Очікує вставлення сесії в Say Music");
  } catch(e) {$("message").textContent=e.message;}
  finally {$("copySession").disabled=false;}
};

$("copySpotifySession").onclick=async()=>{
 $("copySpotifySession").disabled=true;
 try {const result=await sessionAction("EXPORT_SPOTIFY_SESSION");await navigator.clipboard.writeText(result.text);$("message").textContent=tr("Сесію Spotify скопійовано. Say Music → Налаштування → Акаунт → Вставити сесію.");}
 catch(e){$("message").textContent=e.message;}
 finally{$("copySpotifySession").disabled=false;}
};
