"use strict";
(async () => {
  if(window.top!==window)return;
  const page=new URL(location.href),id=new URLSearchParams(page.hash.slice(1)).get("say-music-job");
  if(!id)return;
  page.hash="";
  const task={id,url:page.href,report:__SAY_REPORT_BINDING__};
  if(location.hostname!=="open.spotify.com"){await window[task.report](JSON.stringify({type:"error",id:task.id,error:"Spotify вимагає входу. Увійди у браузері та скопіюй сесію повторно"}));return;}
  const idPattern=/^[A-Za-z0-9]{22}$/;
  const rowSelector='[data-testid="tracklist-row"], [role="row"][aria-rowindex]';
  let indexOffset=1;
  let collecting=false, cancelled=false, scanTimer, polling=false;
  const text=n=>(n?.textContent || "").replace(/\s+/g," ").trim();
  const trackID=href=>{try{const u=new URL(href,location.href);const m=u.pathname.match(/\/(?:track)\/([A-Za-z0-9]{22})(?:\/|$)/);return u.hostname==="open.spotify.com" && m ? m[1] : null;}catch{return null;}};
  const wait=ms=>new Promise(r=>setTimeout(r,ms));
  let lastNotice="",lastNoticeAt=0;

  // Runs inside the player's dedicated WebView2, never inside the extension.
  async function send(message) {
    const report=message.type==="SPOTIFY_IMPORT" ? {type:"result",title:message.title,tracks:message.tracks,gaps:message.gaps||[],complete:message.complete} : {type:"error",error:message.error};
    await window[task.report](JSON.stringify({...report,id:task.id}));
    return {ok:true,message:"Список передано плеєру"};
  }
  function notice(message) { const now=Date.now();if(message===lastNotice || now-lastNoticeAt<750)return;lastNotice=message;lastNoticeAt=now;window[task.report](JSON.stringify({type:"progress",message,id:task.id})).catch(()=>{}); }
  function metadata(row) {
    const links=[...row.querySelectorAll('a[href*="/track/"]')];
    const link=links.find(a=>trackID(a.href) && text(a));
    const cell=row.querySelector('[aria-colindex="2"]');
    const titleNode=link || row.querySelector('[data-testid="internal-track-link"],[data-testid="track-title"]') || cell?.querySelector('[dir="auto"],[data-encore-id="text"]');
    const title=text(titleNode);
    if(!title)return null;
    const artists=[...row.querySelectorAll('a[href*="/artist/"]')].map(text).filter(Boolean);
    if(!artists.length){const n=row.querySelector('[data-testid="artist-name"]');if(n)artists.push(text(n));}
    const durations=text(row).match(/(?:\d{1,2}:)?\d{1,2}:\d{2}/g) || [];
    const duration=durations.length ? durations.at(-1).split(":").reduce((n,v)=>n*60+Number(v),0) : 0;
    return {id:link ? trackID(link.href) : "",title,artist:[...new Set(artists)].join(", "),thumbnail:row.querySelector("img")?.src || "",duration,unavailable:row.getAttribute("aria-disabled")==="true" || row.getAttribute("data-is-unavailable")==="true",position:position(row)};
  }
  function position(row) {
    const ordinal=text(row.querySelector('[aria-colindex="1"]'));
    if(/^\d+$/.test(ordinal) && Number(ordinal)>0)return Number(ordinal);
    const index=Number(row.getAttribute("aria-rowindex") || row.closest("[aria-rowindex]")?.getAttribute("aria-rowindex"));
    if(index>0){const grid=row.closest('[role="grid"],[role="table"]');const hasHeader=!!grid?.querySelector('[role="columnheader"]');return Math.max(1,index-(hasHeader ? 1 : indexOffset));}
    return null;
  }
  function trackRows() {
    return [...document.querySelectorAll(rowSelector)].filter(row=>{
      if(row.querySelector('[role="columnheader"]'))return false;
      const ordinal=text(row.querySelector('[aria-colindex="1"]'));
      return row.matches('[data-testid="tracklist-row"]') || !!row.querySelector('a[href*="/track/"]') || /^\d+$/.test(ordinal) || (row.hasAttribute("aria-rowindex") && /\d{1,2}:\d{2}/.test(text(row)));
    });
  }
  function scroller(row) {
    for(let n=row?.parentElement;n;n=n.parentElement) {
      if(n.scrollHeight>n.clientHeight+2 && /auto|scroll/.test(getComputedStyle(n).overflowY))return n;
    }
    return document.querySelector('[data-overlayscrollbars-viewport],.main-view-container__scroll-node') || document.scrollingElement;
  }
  const pageTitle=()=>text(document.querySelector("main h1")) || (location.pathname.includes("collection/tracks") ? "Уподобані Spotify" : document.title.replace(/\s*[|–-]\s*Spotify.*$/i,""));
  async function collect(url,request="") {
    if(collecting)throw Error("Збір Spotify вже виконується");
    const target=new URL(url), current=new URL(location.href);
    if(target.pathname.replace(/^\/intl-[^/]+/,"").replace(/\/$/,"")!==current.pathname.replace(/^\/intl-[^/]+/,"").replace(/\/$/,""))throw Error("Відкрий потрібний плейлист або пісню у вкладці Spotify");
    collecting=true;cancelled=false;
    let scroll,original=0;
    const pathname=location.pathname;
    try {
      notice("Spotify: читаю метадані…",true);
      const single=target.pathname.match(/\/track\/([A-Za-z0-9]{22})/);
      let first;
      for(let i=0;i<240;i++) {
        if(cancelled || location.pathname!==pathname)throw Error("Збір Spotify скасовано");
        first=trackRows()[0];
        if(first || (single && document.querySelector("main h1")))break;
        await wait(250);
      }
      if(document.querySelector('[data-testid="login-button"]') && location.pathname.includes("collection/tracks"))throw Error("Сесія Spotify застаріла. Увійди у браузері та скопіюй її повторно");
      if(single) {
        let item=[...document.querySelectorAll(rowSelector)].map(metadata).find(t=>t?.id===single[1]);
        if(!item) {
          const root=document.querySelector("main");
          const title=text(root?.querySelector("h1")),artists=[...root?.querySelectorAll('a[href*="/artist/"]') || []].map(text).filter(Boolean);
          if(!title || !artists.length)throw Error("Не вдалося прочитати пісню Spotify. Відкрий її сторінку або додай з рядка плейлиста");
          item={id:single[1],title,artist:[...new Set(artists)].join(", "),thumbnail:root.querySelector("img")?.src || "",duration:0};
        }
        const result=await send({type:"SPOTIFY_IMPORT",url,title:pageTitle(),tracks:[item],complete:true});notice(result.message);return;
      }
      if(!first && (document.querySelector('main [role="grid"][aria-rowcount="1"],main [data-testid="empty-state"]')) && !document.querySelector('[data-testid="login-button"]')) {
        const r=await send({type:"SPOTIFY_IMPORT",url,title:pageTitle(),tracks:[],complete:true,request});notice(r.message);return;
      }
      if(!first)throw Error("Не знайдено список пісень. Увійди у Spotify та відкрий плейлист або «Уподобані»");
      scroll=scroller(first);if(!scroll)throw Error("Не знайдено область списку Spotify");
      original=scroll.scrollTop;scroll.scrollTop=0;scroll.dispatchEvent(new Event("scroll"));await wait(400);
      const firstIndex=Number(trackRows()[0]?.getAttribute("aria-rowindex"));
      indexOffset=firstIndex===1 ? 0 : 1;
      const observed=new Map();
      let expected=0,ariaOffset=0,declaredTotal=0;
      function read() {
        for(const row of trackRows()) {
          const rank=position(row);if(!rank)continue;
          const ordinal=text(row.querySelector('[aria-colindex="1"]')),index=Number(row.getAttribute("aria-rowindex"));
          if(index>=rank && index>0)ariaOffset=index-rank;
          const item=metadata(row),old=observed.get(rank);
          if(!old || (item && (!old.item || !old.item.id || item.id))) observed.set(rank,{item,reason:row.getAttribute("aria-disabled")==="true" ? "Пісня недоступна у Spotify" : "Spotify не надав назву або посилання для цієї позиції"});
        }
        const grid=document.querySelector('main [role="grid"][aria-rowcount],main [role="table"][aria-rowcount],[data-testid="playlist-tracklist"][aria-rowcount]');
        const count=Number(grid?.getAttribute("aria-rowcount"));
        if(!ariaOffset && grid?.querySelector('[role="columnheader"]'))ariaOffset=1;
        if(count>0){declaredTotal=count-ariaOffset;expected=Math.max(expected,declaredTotal);}
        if(observed.size)expected=Math.max(expected,...observed.keys());
        if(expected>10000 || observed.size>10000)throw Error("У списку понад 10000 позицій — збір зупинено");
        const readable=[...observed.values()].filter(v=>v.item).length;
        notice("Spotify: прочитано "+readable+" / "+expected+" пісень; перевірено позицій — "+observed.size);
      }
      function checkCancel(){if(cancelled || location.pathname!==pathname)throw Error("Збір Spotify скасовано; попередній список збережено");}
      async function sweep(direction) {
        let stable=0,stamp="";
        for(let iteration=0;iteration<6000;iteration++) {
          checkCancel();read();
          if(declaredTotal>0 && observed.size>=expected && [...observed.values()].every(v=>v.item))return;
          const edge=direction>0 ? scroll.scrollTop+scroll.clientHeight>=scroll.scrollHeight-3 : scroll.scrollTop<=1;
          const next=[scroll.scrollTop,scroll.scrollHeight,observed.size].join(":");
          const loading=!!document.querySelector('main [role="progressbar"],main [data-testid="loading-indicator"]');
          stable=edge && !loading && stamp===next ? stable+1 : 0;stamp=next;
          if(stable>=3)return;
          // Overlap visible rows and wait for lazy/virtualized rows to settle.
          scroll.scrollTop=Math.max(0,Math.min(scroll.scrollHeight,scroll.scrollTop+direction*Math.max(24,scroll.clientHeight*.7)));
          scroll.dispatchEvent(new Event("scroll"));await wait(200);
        }
        throw Error("Spotify не завершив завантаження списку. Повтори синхронізацію");
      }
      await sweep(1);
      const holes=()=>Array.from({length:expected},(_,i)=>i+1).filter(rank=>!observed.get(rank)?.item);
      // Retry only a bounded set of missing positions. Full reverse sweeps
      // made large collections appear stuck at N/N for several more minutes.
      const retryStart=Date.now(),missing=holes();
      for(const rank of missing.slice(0,16)) {
        if(Date.now()-retryStart>=8000)break;
        checkCancel();
        notice("Spotify: повторна перевірка пропущених позицій ("+holes().length+"); решта потрапить у проблемні пісні…");
        scroll.scrollTop=Math.max(0,scroll.scrollHeight*(rank-1)/Math.max(1,expected)-scroll.clientHeight*.4);
        scroll.dispatchEvent(new Event("scroll"));await wait(250);read();
        if(!observed.get(rank)?.item){scroll.scrollTop+=Math.max(24,scroll.clientHeight*.2);scroll.dispatchEvent(new Event("scroll"));await wait(200);read();}
      }
      if(expected<1)throw Error("Не вдалося визначити позиції Spotify; попередній список збережено");
      const tracks=[],gaps=[],seen=new Set();
      for(let rank=1;rank<=expected;rank++) {
        const entry=observed.get(rank),item=entry?.item;
        if(item) {
          const key=item.id || item.title+"\0"+item.artist;
          if(!seen.has(key)){seen.add(key);tracks.push({...item,position:rank});}
        } else gaps.push({position:rank,title:"",artist:"",reason:entry?.reason || "Цю позицію не вдалося прочитати після повторних проходів Spotify"});
      }
      // An entirely unreadable page is not an empty library.
      // Even an entirely unreadable list is reported as gaps; never replace it with an empty collection.
      checkCancel();
      notice("Spotify: збір завершено, передавання "+tracks.length+" пісень у бібліотеку…");
      const result=await send({type:"SPOTIFY_IMPORT",url,title:pageTitle(),tracks,gaps,complete:gaps.length===0,request});
      const fallback=tracks.filter(t=>t.unavailable || !t.id).length;
      notice(result.message+(gaps.length || fallback ? " • Проблемні позиції: Налаштування → Додатково → Проблемні пісні" : ""));
      return;
    } catch(e) {
      notice(e.message);
      send({type:"SPOTIFY_ERROR",request,url,error:e.message}).catch(()=>{});
      throw e;
    } finally {if(scroll) {scroll.scrollTop=original;scroll.dispatchEvent(new Event("scroll"));} collecting=false;}
  }

  if(document.readyState==="loading") await new Promise(r=>document.addEventListener("DOMContentLoaded",r,{once:true}));
  await collect(task.url,task.id).catch(()=>{});
})();
