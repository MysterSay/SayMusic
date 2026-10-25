"use strict";
(() => {
 const siteURL=href=>{try{const u=new URL(href,location.href);return u.origin==="https://open.spotify.com" && /^\/(?:intl-[^/]+\/)?(?:track\/[A-Za-z0-9]{22}|playlist\/[A-Za-z0-9]{22}|album\/[A-Za-z0-9]{22}|collection\/tracks)\/?$/.test(u.pathname) ? u.href : null;}catch{return null;}};
 function notice(text){let n=document.getElementById("say-music-notice");if(!n){n=document.createElement("div");n.id="say-music-notice";n.setAttribute("role","status");document.documentElement.append(n);}n.textContent=text;}
 async function add(url){notice("Передаю посилання плеєру…");const r=await chrome.runtime.sendMessage({type:"ADD",url});if(!r?.ok)throw Error(r?.error || "Відкрий Say Music");notice(r.message || "Плеєр отримує список Spotify. Цю вкладку можна закрити.");return r;}
 function makeButton(label,url,className){const b=document.createElement("button");b.type="button";b.className="say-music-add say-music-spotify-theme "+className;b.textContent=label;b.title="Додати у Say Music";b.onclick=async e=>{e.preventDefault();e.stopPropagation();if(b.dataset.inLibrary==="true")return;b.disabled=true;try{await add(typeof url==="function" ? url() : url);window.SayMembership?.refresh();}catch(e){notice(e.message);}finally{b.disabled=false;}};return b;}
 function scan(){
  for(const row of document.querySelectorAll('[data-testid="tracklist-row"], [role="row"][aria-rowindex]')){
   const link=[...row.querySelectorAll('a[href*="/track/"]')].find(a=>siteURL(a.href));if(!link)continue;
   let b=row.querySelector('.say-music-spotify-add');if(b && b.dataset.url!==link.href){b.remove();b=null;}
   if(!b){b=makeButton("＋",link.href,"say-music-track-add say-music-spotify-add");b.dataset.url=link.href;b.dataset.spotifyId=new URL(link.href).pathname.split("/").pop();const cell=row.querySelector('[data-testid="track-duration"], [aria-colindex="5"], [aria-colindex="4"]') || row.lastElementChild; if(cell && cell!==link && !cell.contains(link))cell.prepend(b);else row.append(b);}
  }
  const h=document.querySelector("main h1");if(h && siteURL(location.href) && !document.getElementById("say-music-spotify-page")){const b=makeButton("＋ У Say Music",()=>location.href,"say-music-share-add");b.id="say-music-spotify-page";h.after(b);}
 }
 let timer;new MutationObserver(()=>{clearTimeout(timer);timer=setTimeout(scan,200);}).observe(document.documentElement,{childList:true,subtree:true});scan();
 // Only a URL crosses the bridge. Pagination and authorization belong to the player.
})();
