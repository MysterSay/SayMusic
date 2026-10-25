"use strict";
(() => {
  const ROWS =
    "ytmusic-responsive-list-item-renderer,ytmusic-playlist-panel-video-renderer";
  const sent = new Set();
  let timer;
  const validID = (id) => /^[\w-]{11}$/.test(id || "");
  function normalize(value) {
    try {
      const u = new URL(value, location.href);
      if (
        u.protocol !== "https:" ||
        ![
          "music.youtube.com",
          "www.youtube.com",
          "youtube.com",
          "youtu.be",
        ].includes(u.hostname)
      )
        return null;
      return u;
    } catch {
      return null;
    }
  }
  function videoID(row) {
    for (const a of row.querySelectorAll(
      'a[href*="watch?"],a[href*="youtu.be/"]',
    )) {
      const u = normalize(a.href);
      const id =
        u?.hostname === "youtu.be"
          ? u.pathname.slice(1)
          : u?.searchParams.get("v");
      if (validID(id)) return id;
    }
    const attr =
      row.getAttribute("video-id") || row.getAttribute("data-video-id");
    if (validID(attr)) return attr;
    const image = row.querySelector("img");
    const m = (image?.src || "").match(/\/vi(?:_webp)?\/([\w-]{11})\//);
    return m?.[1] || null;
  }
  const text = (node) => (node?.textContent || "").replace(/\s+/g, " ").trim();
  function metadata(row, id) {
    const title =
      text(row.querySelector('.title-column a,.title a,a[href*="watch?"]')) ||
      text(row.querySelector("yt-formatted-string.title,.title"));
    const artist = text(
      row.querySelector(
        '.secondary-flex-columns a:not([href*="watch"]),.secondary-flex-columns,.byline',
      ),
    );
    const durations = text(row).match(/(?:\d{1,2}:)?\d{1,2}:\d{2}/g) || [];
    let duration = 0;
    if (durations.length)
      duration = durations
        .at(-1)
        .split(":")
        .reduce((s, n) => s * 60 + Number(n), 0);
    const img = row.querySelector("img");
    return {
      type: "ADD",
      url: "https://music.youtube.com/watch?v=" + id,
      title,
      artist,
      duration,
      thumbnail: img?.currentSrc || img?.src || "",
    };
  }
  async function send(button, message, id) {
    if (button.disabled || button.dataset.inLibrary==="true") return;
    button.disabled = true;
    const label = button.textContent;
    const link = button.dataset.link;
    const current = () =>
      id ? button.dataset.videoId === id : button.dataset.link === link;
    button.textContent = "…";
    try {
      const result = await chrome.runtime.sendMessage(message);
      if (!result?.ok)
        throw Error(result?.error || "Немає відповіді від Say Music");
      if (id) sent.add(id);
      window.SayMembership?.refresh();
      if (current()) {
        button.textContent = "✓";
        button.title = result.message || "Додано до Say Music";
      }
      toast(result.message || "Надіслано до Say Music");
    } catch (e) {
      if (current()) {
        button.textContent = label;
        button.title = e.message;
      }
      toast(e.message, true);
    } finally {
      button.disabled = false;
    }
  }
  function bindButton(button, action) {
    button.type = "button";
    button.addEventListener("pointerdown", (e) => e.stopPropagation());
    button.addEventListener("click", (e) => {
      e.preventDefault();
      e.stopPropagation();
      action();
    });
  }
  function scanRows() {
    for (const row of document.querySelectorAll(ROWS)) {
      const id = videoID(row);
      if (!id) continue;
      let button = row.querySelector(".say-music-track-add");
      if (!button) {
        const like = row.querySelector(
          "ytmusic-like-button-renderer,ytmusic-toggle-button-renderer#like-button,#like-button",
        );
        if (!like) continue;
        button = document.createElement("button");
        button.className = "say-music-add say-music-track-add";
        button.setAttribute("aria-label", "Додати трек у Say Music");
        bindButton(button, () => {
          const current = videoID(row);
          if (current) send(button, metadata(row, current), current);
        });
        like.before(button);
      }
      if (button.dataset.videoId !== id) {
        button.dataset.inLibrary="";
        button.dataset.videoId = id;
        button.textContent = sent.has(id) ? "✓" : "＋";
        button.title = sent.has(id)
          ? "Уже надіслано до Say Music"
          : "Додати трек у Say Music";
      }
    }
  }
  function shareInfo(panel) {
    for (const input of panel.querySelectorAll("input,textarea")) {
      const u = normalize(input.value || input.getAttribute("value"));
      if (
        u &&
        (u.searchParams.get("list") ||
          validID(u.searchParams.get("v")) ||
          u.hostname === "youtu.be")
      )
        return { input, url: u.href };
    }
    return null;
  }
  function scanShares() {
    const panels = document.querySelectorAll(
      'ytmusic-share-panel-renderer,yt-share-panel-renderer,ytd-unified-share-panel-renderer,yt-share-dialog-renderer,tp-yt-paper-dialog,[role="dialog"]',
    );
    const seen = new Set();
    for (const panel of panels) {
      const info = shareInfo(panel);
      if (!info || seen.has(info.input)) continue;
      seen.add(info.input);
      const key = info.url;
      let button = panel.querySelector(".say-music-share-add");
      if (!button) {
        button = document.createElement("button");
        button.className = "say-music-add say-music-share-add";
        bindButton(button, () => {
          const latest = shareInfo(panel);
          if (!latest) return;
          const u = normalize(latest.url);
          const id = u?.searchParams.get("v");
          const row = id
            ? [...document.querySelectorAll(ROWS)].find(
                (r) => videoID(r) === id,
              )
            : null;
          const data =
            row && !u.searchParams.get("list")
              ? metadata(row, id)
              : {
                  type: "ADD",
                  url: latest.url,
                  kind: u.searchParams.get("list") ? "playlist" : "track",
                  title: u.searchParams.get("list")
                    ? text(
                        document.querySelector(
                          "ytmusic-detail-header-renderer h2,ytmusic-responsive-header-renderer h1,ytmusic-detail-header-renderer .title,ytmusic-responsive-header-renderer .title",
                        ),
                      )
                    : "",
                };
          data.url = latest.url;
          send(button, data);
        });
        const box =
          info.input.closest(
            "#copy-link,#copy-link-container,.copy-link-container",
          ) || info.input.parentElement;
        box.after(button);
      }
      if (button.dataset.link !== key) {
        button.dataset.link = key;
        button.textContent = "Додати трек/плейлист";
        button.title = "Додати у Say Music";
      }
    }
  }
  function toast(message, error = false) {
    let node = document.getElementById("say-music-notice");
    if (!node) {
      node = document.createElement("div");
      node.id = "say-music-notice";
      node.setAttribute("role", "status");
      document.documentElement.append(node);
    }
    node.textContent = message;
    node.classList.toggle("say-error", error);
    node.hidden = false;
    clearTimeout(node.hideTimer);
    node.hideTimer = setTimeout(() => (node.hidden = true), 6500);
  }
  function pageInfo() {
    const u=new URL(location.href),list=u.searchParams.get("list");
    const title=text(document.querySelector("ytmusic-detail-header-renderer h2,ytmusic-responsive-header-renderer h1,ytmusic-detail-header-renderer .title,ytmusic-responsive-header-renderer .title,h1"));
    const playlist=!!list && u.pathname!=="/watch";
    return {ok:true,url:playlist ? "https://music.youtube.com/playlist?list="+encodeURIComponent(list) : location.href,title,kind:playlist ? "playlist" : "track"};
  }
  function scanPlaylist() {
    const info=pageInfo();
    let b=document.querySelector(".say-music-playlist-add");
    if(info.kind!=="playlist") {if(b)b.remove();return;}
    const header=document.querySelector("ytmusic-detail-header-renderer,ytmusic-responsive-header-renderer,ytd-playlist-header-renderer");
    if(!header)return;
    if(!b) {b=document.createElement("button");b.className="say-music-add say-music-share-add say-music-playlist-add";b.textContent="＋ Плейлист у Say Music";bindButton(b,()=>send(b,{type:"ADD",...pageInfo()}));header.append(b);}
  }
  if(chrome.runtime.onMessage) chrome.runtime.onMessage.addListener((message,sender,respond)=> {
    if(message.type==="PAGE_INFO") respond(pageInfo());
  });
  function scan() {
    scanRows();
    scanShares();
    scanPlaylist();
  }
  function schedule() {
    clearTimeout(timer);
    timer = setTimeout(scan, 120);
  }
  new MutationObserver(schedule).observe(document.documentElement, {
    childList: true,
    subtree: true,
    attributes: true,
    attributeFilter: ["href", "video-id", "data-video-id", "value"],
  });
  document.addEventListener("yt-navigate-finish", schedule);
  window.addEventListener("popstate", schedule);
  setInterval(scanShares, 1200);
  scan();
  // Explicit connection test opened by the desktop setup wizard. No cookies are read.
  if (new URL(location.href).searchParams.get("say_music_setup") === "1") {
    chrome.runtime.sendMessage({type:"STATUS"}).then(r => {
      toast(r?.ok && r.running ? "Say Music: зв’язок перевірено. Повернися до програми." : r?.error || "Відкрий Say Music і повтори перевірку", !r?.ok || !r.running);
    }).catch(e => toast(e.message, true));
  }
})();
