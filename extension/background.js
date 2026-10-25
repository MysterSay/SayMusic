"use strict";
const HOST = "com.saymusic.desktop";
const PAIR_URL = "http://127.0.0.1:17843/pair/bridge";
const storageReady = chrome.storage.local.setAccessLevel({accessLevel:"TRUSTED_CONTEXTS"});
function randomHex(n) { return Array.from(crypto.getRandomValues(new Uint8Array(n)), b => b.toString(16).padStart(2,"0")).join(""); }
function b64(bytes) { let s=""; for(const b of bytes) s+=String.fromCharCode(b); return btoa(s).replace(/\+/g,"-").replace(/\//g,"_").replace(/=+$/,""); }
function unb64(s) { return Uint8Array.from(atob(s.replace(/-/g,"+").replace(/_/g,"/")),c=>c.charCodeAt(0)); }
async function pairCrypto(key) {
  if(!/^[a-f0-9]{64}$/.test(key)) throw Error("Некоректний ключ. Повторно скопіюй і встав сесію.");
  return crypto.subtle.importKey("raw", Uint8Array.from(key.match(/../g), b=>parseInt(b,16)), "AES-GCM", false, ["encrypt","decrypt"]);
}
async function paired(message, key) {
  const cryptoKey=await pairCrypto(key), id=randomHex(16), iv=crypto.getRandomValues(new Uint8Array(12));
  const data=await crypto.subtle.encrypt({name:"AES-GCM",iv,additionalData:new TextEncoder().encode("say-music-request-v1")},cryptoKey,new TextEncoder().encode(JSON.stringify({id,time:Date.now(),message})));
  let response;
  try {
    response=await fetch(PAIR_URL,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({version:1,iv:b64(iv),data:b64(new Uint8Array(data))}),credentials:"omit",redirect:"error",signal:AbortSignal.timeout(5000)});
  } catch {
    if(message.action==="status") return {ok:true,running:false,paired:true};
    throw Error("Відкрий Say Music. Для цього способу з’єднання програма має бути запущена; за потреби натисни «Повторити з’єднання» у її налаштуваннях.");
  }
  if(!response.ok) throw Error(response.status===401 ? "Ключ з’єднання не збігається. Скопіюй сесію з цього розширення та встав у Say Music повторно." : "Say Music відхилила повідомлення. Перевір час на ПК та повтори дію.");
  try {
    const envelope=await response.json();
    if(envelope.version!==1) throw Error("version");
    const plain=await crypto.subtle.decrypt({name:"AES-GCM",iv:unb64(envelope.iv),additionalData:new TextEncoder().encode("say-music-response-v1")},cryptoKey,unb64(envelope.data));
    const result=JSON.parse(new TextDecoder().decode(plain));
    if(result.id!==id || !result.reply) throw Error("id");
    if(!result.reply.ok) throw Error(result.reply.error || "Say Music не підтвердила дію");
    return {...result.reply,paired:true};
  } catch(e) { throw Error(e.message || "Не вдалося перевірити відповідь Say Music"); }
}
async function desktop(message) {
  await storageReady;
  const {pairingKey}=await chrome.storage.local.get("pairingKey");
  if(pairingKey) return paired(message,pairingKey);
  return native(message);
}
// Authentication is available only to the extension's own popup, with a
// temporary permission granted by a user click. Page scripts cannot export it.
function requirePopup(sender) {
 if(sender.tab || sender.url!==chrome.runtime.getURL("popup.html"))throw Error("Ця дія доступна лише кнопкою у вікні Say Music");
}
async function withSessionPermission(sender,operation) {
 requirePopup(sender);
 if(!await chrome.permissions.contains({permissions:["cookies"]}))throw Error("Натисни кнопку підключення та дозволь тимчасовий доступ до сесії");
 try{return await operation();}
 finally {
  const removed=await chrome.permissions.remove({permissions:["cookies"]});
  if(!removed || await chrome.permissions.contains({permissions:["cookies"]}))throw Error("Не вдалося відкликати доступ до cookies. Вимкни розширення та перевір його дозволи");
 }
}
function sessionCookie(c,domains) {
 return domains.includes(c.domain.replace(/^\./,"")) && (!c.expirationDate || c.expirationDate>Date.now()/1000);
}
async function youtubeCookies() {
  const cookies=await chrome.cookies.getAll({domain:"youtube.com"});
  const names=new Set(["SAPISID","APISID","SID","HSID","SSID","SIDCC","LOGIN_INFO","__Secure-1PAPISID","__Secure-3PAPISID","__Secure-1PSID","__Secure-3PSID","__Secure-1PSIDTS","__Secure-3PSIDTS","__Secure-1PSIDCC","__Secure-3PSIDCC","CONSENT","SOCS","PREF","YSC","VISITOR_INFO1_LIVE","VISITOR_PRIVACY_METADATA"]);
  const youtube=cookies.filter(c=>names.has(c.name) && sessionCookie(c,["youtube.com","www.youtube.com","music.youtube.com"]));
  if(!youtube.some(c=>["SAPISID","__Secure-3PAPISID"].includes(c.name))) throw Error("Спочатку увійди у Google на music.youtube.com у цьому профілі браузера.");
  return youtube.map(c=>({name:c.name,value:c.value,domain:c.domain,path:c.path,secure:c.secure,httpOnly:c.httpOnly,expires:c.expirationDate||0}));
}
function spotifyURL(value) {
 try {const u=new URL(value);return u.protocol==="https:" && u.hostname==="open.spotify.com" && !u.username && !u.password && !u.port && /^\/(?:intl-[^/]+\/)?(?:track\/[A-Za-z0-9]{22}|playlist\/[A-Za-z0-9]{22}|album\/[A-Za-z0-9]{22}|collection\/tracks)\/?$/.test(u.pathname) ? u.href : null;}catch{return null;}
}
function spotifySite(value) {try{const u=new URL(value);return u.protocol==="https:" && u.hostname==="open.spotify.com";}catch{return false;}}
async function spotifySession() {
 const cookies=(await chrome.cookies.getAll({domain:"spotify.com"})).filter(c=>["sp_dc","sp_key","sp_t"].includes(c.name) && sessionCookie(c,["spotify.com","open.spotify.com"]));
 if(!cookies.some(c=>(c.domain==="spotify.com"||c.domain.endsWith(".spotify.com")) && c.name==="sp_dc" && c.value)) throw Error("Увійди у Spotify на open.spotify.com у цьому профілі браузера.");
 return cookies.map(c=>({name:c.name,value:c.value,domain:c.domain,path:c.path||"/",secure:c.secure,httpOnly:c.httpOnly,expires:c.expirationDate||0}));
}
async function sessionExport(provider) {
 const cookies=provider==="spotify" ? await spotifySession() : await youtubeCookies();
 await storageReady;
 let {pairingKey}=await chrome.storage.local.get("pairingKey");
 if(!pairingKey) {pairingKey=randomHex(32);await chrome.storage.local.set({pairingKey});}
 return {ok:true,text:"SAYMUSIC_SESSION_V1\n"+JSON.stringify({format:"say-music-session",version:1,createdAt:Date.now(),pairingKey,provider,cookies})};
}
function youtubeURL(value) {
  try {
    const u = new URL(value);
    return u.protocol === "https:" && !u.username && !u.password && !u.port &&
      [
        "music.youtube.com",
        "www.youtube.com",
        "youtube.com",
        "youtu.be",
      ].includes(u.hostname)
      ? u.href
      : null;
  } catch {
    return null;
  }
}
async function native(message) {
  try {
    const result = await chrome.runtime.sendNativeMessage(HOST, message);
    if (!result?.ok)
      throw Error(result?.error || "Say Music не підтвердив дію");
    return result;
  } catch (e) {
    const text = e.message || String(e);
    if (
      /not found|not registered|forbidden|host has exited|failed to start/i.test(
        text,
      )
    )
      throw Error(
        "Не встановлено з’єднання. У Say Music відкрий Налаштування → Розширення → Встановити зв’язок. Потім перезапусти браузер. Деталі: " + text,
      );
    throw e;
  }
}
async function handle(message, sender) {
  if (sender.id !== chrome.runtime.id) throw Error("Недозволений відправник");
  if (sender.tab && !youtubeURL(sender.url || sender.tab.url) && !spotifySite(sender.url || sender.tab.url))
    throw Error("Підтримуються YouTube і Spotify");
  switch (message.type) {
    case "CHECK_LIBRARY": {
      const provider=message.provider;
      if(!["youtube","spotify"].includes(provider) || !Array.isArray(message.ids) || message.ids.length>200 || message.ids.some(id=>typeof id!=="string" || !(provider==="youtube" ? /^[\w-]{11}$/ : /^[A-Za-z0-9]{22}$/).test(id)))throw Error("Некоректний запит");
      if(sender.tab && ((provider==="spotify")!==spotifySite(sender.url || sender.tab.url)))throw Error("Некоректний запит");
      return desktop({action:"library/check",provider,ids:[...new Set(message.ids)]});
    }
    case "ADD": {
      const spotify=spotifyURL(message.url);
      if(spotify) {
        return desktop({action:"spotify/fetch",url:spotify});
      }
      let link = youtubeURL(message.url);
      if(link) {const u=new URL(link);if(u.searchParams.get("v") && message.kind!=="playlist") {u.searchParams.delete("list");u.searchParams.delete("index");link=u.href;} else if(u.searchParams.get("list")) link="https://music.youtube.com/playlist?list="+encodeURIComponent(u.searchParams.get("list"));}
      if (!link) throw Error("Немає посилання на трек або плейлист");
      return desktop({
        action: "add",
        url: link,
        title: String(message.title || "").slice(0, 500),
        artist: String(message.artist || "").slice(0, 300),
        thumbnail: String(message.thumbnail || "").slice(0, 1500),
        duration: Math.min(86400, Math.max(0, Number(message.duration) || 0)),
      });
    }
    case "AUTH":
      return withSessionPermission(sender,async()=>desktop({action:"auth",cookies:await youtubeCookies()}));
    case "EXPORT_SESSION":
      return withSessionPermission(sender,()=>sessionExport("youtube"));
    case "EXPORT_SPOTIFY_SESSION":
      return withSessionPermission(sender,()=>sessionExport("spotify"));
    case "AUTH_SPOTIFY":
      return withSessionPermission(sender,async()=>desktop({action:"spotify/auth",cookies:await spotifySession()}));
    case "STATUS":
      return desktop({ action: "status" });
    case "OPEN":
      requirePopup(sender);
      return desktop({ action: "open" });
    default:
      throw Error("Невідома дія");
  }
}
chrome.runtime.onMessage.addListener((message, sender, respond) => {
  handle(message, sender)
    .then((v) => respond(v))
    .catch((e) => respond({ ok: false, error: e.message || String(e) }));
  return true;
});

// Revoke a previously granted cookie permission when installing/updating.
chrome.runtime.onInstalled?.addListener(()=>{
 chrome.permissions.remove({permissions:["cookies"]}).catch(()=>{});
});
