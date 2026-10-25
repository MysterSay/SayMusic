"use strict";
const tr = (s) => window.SayI18n ? window.SayI18n.t(s) : s;
const $ = (s) => document.getElementById(s),
  token =
    new URLSearchParams(location.search).get("token") ||
    sessionStorage.getItem("say-music-token");
sessionStorage.setItem("say-music-token", token || "");
history.replaceState(null, "", "/");
const audio = $("audio");
let library = [],
  lists = [],
  favorites = [],
  favoriteIDs = new Set(),
  favoriteBusyIDs = new Set(),
  visible = [],
  queue = [],
  index = -1,
  view = "library",
  playlist = 0,
  searchQuery = "",
  offset = 0,
  repeat = "off",
  shuffle = false,
  pickIDs = [],
  dragID = null,
  toastTimer,
  playGeneration = 0,
  saveTimer,
  radioSeed = "",
  radioBusy = false;
let settings = {},
  plannedNext = "",
  prefetched = "",
  prefetchBusy = false,
  lastPrefetchCheck = 0,
  cachePolling = false;
let artistList = [],
  selectedArtist = "",
  experiencePolling = false,
  commandAfter = 0,
  recommendationBusy = false;
const url = (p) =>
  p + (p.includes("?") ? "&" : "?") + "token=" + encodeURIComponent(token);
async function api(path, data) {
  const r = await fetch(url("/api/" + path), {
    method: data === undefined ? "GET" : "POST",
    headers: data === undefined ? {} : { "Content-Type": "application/json" },
    body: data === undefined ? undefined : JSON.stringify(data),
  });
  let v;
  try {
    v = await r.json();
  } catch {
    throw Error(tr("Сервер не відповів: ") + r.status);
  }
  if (!r.ok) throw Error(tr(v.error || r.statusText));
  return v;
}
function toast(s) {
  $("toast").textContent = tr(s);
  $("toast").hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => ($("toast").hidden = true), 6500);
}
function safely(fn) {
  return async (...args) => {
    try {
      await fn(...args);
    } catch (e) {
      toast(e.message);
    }
  };
}
function time(s) {
  s = Math.max(0, Math.floor(Number(s) || 0));
  return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0");
}
function button(label, title, fn) {
  const b = document.createElement("button");
  b.textContent = label;
  b.title = title;
  b.onclick = safely(fn);
  return b;
}
function el(tag, text, cls) {
  const n = document.createElement(tag);
  if (text !== undefined) n.textContent = text;
  if (cls) n.className = cls;
  return n;
}
function artwork(src) {
  if (settings.offlineOnly) return "/placeholder.svg";
  if (!src) return "";
  try {
    const u = new URL(src);
    if (
      u.protocol === "https:" &&
      ["i.ytimg.com", "lh3.googleusercontent.com", "yt3.ggpht.com", "i.scdn.co", "mosaic.scdn.co"].includes(
        u.hostname,
      )
    )
      return u.href;
  } catch {}
  return "";
}
async function load() {
  [library, lists, favorites] = await Promise.all([api("library"), api("playlists"), api("favorites")]);
  favoriteIDs = new Set(favorites.map(t => t.id));
  updateFavoriteButton($("favoriteNow"), queue[index]?.id);
  renderLists();
}
function sourceBadge(source) {
  const badge = el("span", source === "youtube" ? "▶" : "≋", "source-badge " + source);
  badge.title = source === "youtube" ? "YouTube" : "Spotify";
  badge.setAttribute("aria-label", badge.title);
  return badge;
}
function systemPlaylist(id) { return id === 1 || !!lists.find(p => p.id === id)?.system; }
function renderLists() {
  $("playlists").replaceChildren();
  for (const p of lists.filter((x) => !x.system)) {
    const b = button("", p.name, () => openPlaylist(p.id));
    b.className =
      "playlist-card" +
      (view === "playlist" && playlist === p.id ? " active" : "");
    const label = el("span", undefined, "playlist-label");
    if (p.source) label.append(sourceBadge(p.source));
    label.append(el("span", p.name));
    b.append(label, el("span", String(p.count)));
    $("playlists").append(b);
  }
}
function title(name, sub) {
  $("viewTitle").textContent = name;
  $("listTitle").textContent = name;
  $("subtitle").textContent = sub;
  $("playlists").hidden = view !== "playlists";
  $("favoritesNav").classList.toggle("active", view === "favorites");
  $("playlistsNav").classList.toggle("active", view === "playlists" || (view === "playlist" && !systemPlaylist(playlist)));
  $("home").classList.toggle("active", view === "library");
  $("liked").classList.toggle("active", view === "playlist" && playlist === 1);
  $("spotifyLiked").classList.toggle("active", view === "playlist" && lists.some(p => p.id === playlist && p.source === "spotify" && p.system));
  $("queueNav").classList.toggle("active", view === "queue");
  $("offlineNav").classList.toggle("active", view === "offline");
  const editable = view === "playlist" && !systemPlaylist(playlist);
  $("renamePlaylist").hidden = !editable;
  $("deletePlaylist").hidden = !editable;
  $("artistGrid").hidden = view !== "artists";
  $("artistsNav").classList.toggle(
    "active",
    view === "artists" || view === "artist",
  );
  $("recommendationsNav").classList.toggle(
    "active",
    view === "recommendations",
  );
  $("playAll").disabled = $("shuffleAll").disabled = ["artists", "playlists"].includes(view);
  $("addAll").hidden = $("cacheAll").hidden = ["artists", "playlists"].includes(view);
  renderLists();
}
async function showLibrary() {
  view = "library";
  playlist = 0;
  visible = [...library];
  title(tr("Моя музика"), tr("Твоя особиста колекція • збережено на цьому ПК"));
  render();
}
async function openPlaylist(id) {
  if (view === "playlists") $("filter").value = "";
  view = "playlist";
  playlist = id;
  visible = await api("playlist?id=" + id);
  const p = lists.find((x) => x.id === id);
  title(
    (p?.system ? tr(p.name) : p?.name) || tr("Плейлист"),
    systemPlaylist(id)
      ? (p?.source === "spotify" ? tr("Синхронізуй Spotify через налаштування акаунта") : tr("Синхронізуй із YouTube Music через налаштування"))
      : tr("Локальний плейлист • збережено тільки в Say Music"),
  );
  if (p?.source) $("viewTitle").prepend(sourceBadge(p.source), document.createTextNode(" "));
  render();
}
async function refreshView() {
  await load();
  if (view === "offline") showOffline();
  else if (view === "playlist") await openPlaylist(playlist);
  else if (view === "favorites") showFavorites();
  else if (view === "playlists") showPlaylists();
  else if (view === "queue") showQueue();
  else if (view === "search") await search(true);
  else if (view === "artists") await showArtists();
  else if (view === "artist") await showArtist(selectedArtist);
  else if (view === "recommendations") await showRecommendations(true);
  else await showLibrary();
}
function filtered() {
  const f = $("filter").value.toLocaleLowerCase();
  return visible.filter((t) =>
    (t.title + " " + t.artist).toLocaleLowerCase().includes(f),
  );
}
function updateFavoriteButton(b, id) {
  const saved = favoriteIDs.has(id);
  b.disabled = !id || favoriteBusyIDs.has(id);
  b.textContent = saved ? "♥" : "♡";
  b.classList.add("favorite-button");
  b.classList.toggle("active", saved);
  b.setAttribute("aria-pressed", String(saved));
  b.title = saved ? tr("Прибрати з уподобаних") : tr("Додати до уподобаних");
}
async function setFavorite(id) {
  if (!id || favoriteBusyIDs.has(id)) return;
  favoriteBusyIDs.add(id);
  const favorite = !favoriteIDs.has(id);
  try {
    await api("favorites/set", {id, favorite});
    await load();
    toast(favorite ? tr("Додано до уподобаних") : tr("Прибрано з уподобаних"));
  } finally {
    favoriteBusyIDs.delete(id);
    updateFavoriteButton($("favoriteNow"), queue[index]?.id);
    if (view === "favorites") showFavorites();
    else render();
  }
}
function showFavorites() {
  view = "favorites";
  playlist = 0;
  visible = [...favorites];
  title(tr("Уподобані"), tr("Твої улюблені треки • збережено в Say Music"));
  render();
}
function showPlaylists() {
  view = "playlists";
  playlist = 0;
  visible = [];
  title(tr("Плейлисти"), tr("Усі твої плейлисти • обери, щоб відкрити"));
  render();
}
// Match a recording across providers without confusing live/remix versions or namesakes.
function sameRecording(a,b) {
 if (!a || !b) return false;
 if (a.id === b.id) return true;
 const normalize=s=>String(s || "").normalize("NFKC").toLocaleLowerCase().replace(/[’‘`]/g,"'").replace(/\s+/g," ").trim();
 return !!normalize(a.title) && !!normalize(a.artist) && normalize(a.title)===normalize(b.title) && normalize(a.artist)===normalize(b.artist) && a.duration>0 && b.duration>0 && Math.abs(a.duration-b.duration)<=2;
}
function updatePlayingRows(root=$("tracks")) {
 const shown=new Map(filtered().map(t=>[t.id,t]));
 for (const row of root.querySelectorAll(".row")) {
  const current=sameRecording(queue[index],shown.get(row.dataset.id));
  row.classList.toggle("current",current);
  if (current) row.setAttribute("aria-current","true"); else row.removeAttribute("aria-current");
  const play=row.querySelector(".row-play");
  if(play) { play.textContent=current && !audio.paused ? "❚❚" : "▶"; play.title=current ? (audio.paused ? tr("Продовжити") : tr("Пауза")) : tr("Відтворити"); }
 }
}
let renderEpoch=0, renderedListContext="";
function render() {
  const epoch=++renderEpoch;
  const listContext = JSON.stringify([view, playlist, $("filter").value]);
  const preservePosition = renderedListContext === listContext;
  renderedListContext = listContext;
  $("tracks").removeAttribute("aria-busy");
  if (view === "playlists") {
    const query = $("filter").value.toLocaleLowerCase();
    renderLists();
    for (const card of $("playlists").children)
      card.hidden = !card.title.toLocaleLowerCase().includes(query);
    $("tracks").style.minHeight = "";
    $("tracks").replaceChildren();
    $("count").textContent = lists.filter(p => !p.system && p.name.toLocaleLowerCase().includes(query)).length + tr(" плейлистів");
    $("empty").hidden = true;
    return;
  }
  if (view === "artists") {
    renderArtistCards();
    return;
  }
  const ts = filtered();
  $("count").textContent = ts.length + tr(" треків");
  $("empty").hidden = ts.length > 0;
  const scroller = document.querySelector("main"), trackList = $("tracks");
  const savedScroll = preservePosition ? scroller.scrollTop : 0;
  if (!preservePosition) scroller.scrollTop = 0;
  const viewportTop = scroller.getBoundingClientRect().top;
  const anchor = preservePosition ? [...trackList.children].find(row => row.getBoundingClientRect().bottom > viewportTop) : null;
  const anchorID = anchor?.dataset.id, anchorTop = anchor?.getBoundingClientRect().top;
  const focused = trackList.contains(document.activeElement) ? document.activeElement : null;
  const focusedID = focused?.closest(".row")?.dataset.id;
  const focusedIndex = focused?.closest(".row") ? [...focused.closest(".row").querySelectorAll("button")].indexOf(focused) : -1;
  trackList.style.minHeight = trackList.getBoundingClientRect().height + "px";
  scroller.style.overflowAnchor = "none";
  trackList.replaceChildren();
  $("empty").querySelector("h2").textContent = view === "favorites" ? tr("Тут будуть твої уподобані") : tr("Додай свій перший трек");
  $("empty").querySelector("p").textContent = view === "favorites" ? tr("Натисни сердечко біля треку, у плеєрі або в острівці.") : tr("Введи назву в пошуку або імпортуй посилання на плейлист.");
  function makeRow(t,i) {
    const row = el(
      "div",
      undefined,
      "row" + (sameRecording(queue[index], t) ? " current" : ""),
    );
    row.dataset.id = t.id;
    row.append(el("span", String(i + 1), "number"));
    const img = el("img", undefined, "cover");
    img.src = artwork(t.thumbnail);
    img.alt = "";
    img.loading = "lazy";
    img.onclick = safely(() => playFromList(t.id));
    row.append(img);
    const info = el("div", undefined, "trackInfo");
    info.append(el("strong", t.title), el("span", t.artist || "YouTube"));
    info.onclick = safely(() => playFromList(t.id));
    row.append(info);
    if (t.cached) row.append(el("span", tr("✓ Офлайн"), "cachedBadge"));
    row.append(el("span", time(t.duration), "length"));
    const heart = button("♡", tr("Додати до уподобаних"), () => setFavorite(t.id));
    updateFavoriteButton(heart, t.id);
    row.append(heart);
    row.append(
      Object.assign(button("▶", tr("Відтворити"), () => {
        if (sameRecording(queue[index],t)) return audio.paused ? resumePlayback() : audio.pause();
        return playFromList(t.id);
      }), {className:"row-play"}),
      button("＋", tr("Додати до черги"), () => {
        queue.push(t);
        saveState();
        toast(tr("Додано до черги"));
        if (view === "queue") showQueue();
      }),
      button("▤", tr("Додати у плейлист"), () => pick([t.id])),
      button("↓", tr("Зберегти офлайн"), () => cacheTracks([t.id])),
    );
    if (view === "offline")
      row.append(
        button("×", tr("Видалити аудіокеш"), async () => {
          if (
            confirm(
              tr("Видалити офлайн-файл цього треку? Назва й посилання залишаться."),
            )
          ) {
            await api("cache/remove", { id: t.id });
            await load();
            showOffline();
          }
        }),
      );
    else if (view === "playlist" && !systemPlaylist(playlist))
      row.append(
        button("×", tr("Прибрати з плейлиста"), async () => {
          await api("playlist/remove", { playlist, id: t.id });
          await refreshView();
        }),
      );
    else if (view === "queue")
      row.append(
        button("×", tr("Прибрати з черги"), () => removeQueue(queue.indexOf(t))),
      );
    else if (view === "library")
      row.append(
        button("×", tr("Перемістити у кошик"), async () => {
          if (
            confirm(
              tr("Перемістити «") +
                t.title +
                tr("» у кошик? Пісню можна буде відновити в налаштуваннях."),
            )
          ) {
            await api("track/delete", { id: t.id });
            const currentID = queue[index]?.id;
            if (currentID === t.id) {
              audio.pause();
              audio.removeAttribute("src");
              audio.load();
              playGeneration++;
            }
            queue = queue.filter((x) => x.id !== t.id);
            index =
              currentID === t.id
                ? -1
                : queue.findIndex((x) => x.id === currentID);
            updateNow();
            saveState();
            await refreshView();
          }
        }),
      );
    if (view === "playlist" && !systemPlaylist(playlist)) {
      row.draggable = true;
      row.ondragstart = () => {
        dragID = t.id;
      };
      row.ondragover = (e) => e.preventDefault();
      row.ondrop = safely(async (e) => {
        e.preventDefault();
        const from = visible.findIndex((x) => x.id === dragID),
          to = visible.findIndex((x) => x.id === t.id);
        if (from < 0 || to < 0) return;
        const [item] = visible.splice(from, 1);
        visible.splice(to, 0, item);
        await api("playlist/reorder", {
          playlist,
          ids: visible.map((x) => x.id),
        });
        render();
      });
    }
    return row;
  }
  let cursor=0;
  if(ts.length>150)$("tracks").setAttribute("aria-busy","true");
  function appendChunk(){
    if(epoch!==renderEpoch)return;
    const frag=document.createDocumentFragment(),started=performance.now();
    let batch=0;
    do {
      if(cursor>=ts.length)break;
      frag.append(makeRow(ts[cursor],cursor));cursor++;batch++;
    }while(ts.length<=150 || (batch<75 && performance.now()-started<8));
    updatePlayingRows(frag);
    $("tracks").append(frag);
    if(cursor<ts.length)requestAnimationFrame(appendChunk);
    else {
      const untouched = Math.abs(scroller.scrollTop - savedScroll) < 2;
      const nextAnchor = [...trackList.children].find(row => row.dataset.id === anchorID);
      if (untouched && nextAnchor) scroller.scrollTop += nextAnchor.getBoundingClientRect().top - anchorTop;
      trackList.style.minHeight = "";
      if (untouched && !nextAnchor) scroller.scrollTop = savedScroll;
      if (focusedID && (document.activeElement === document.body || document.activeElement === focused)) {
        const row = [...trackList.children].find(row => row.dataset.id === focusedID);
        row?.querySelectorAll("button")[focusedIndex]?.focus({preventScroll:true});
      }
      $("tracks").removeAttribute("aria-busy");
    }
  }
  appendChunk();
}
function showQueue() {
  view = "queue";
  playlist = 0;
  visible = [...queue];
  title(
    tr("Черга відтворення"),
    radioSeed
      ? tr("Радіо ввімкнено • схожі треки додаються автоматично")
      : tr("Треки відтворюються в цьому порядку"),
  );
  render();
}
function removeQueue(i) {
  plannedNext = "";
  prefetched = "";
  api("cache/prefetch", { id: "" }).catch(() => {});
  if (i === index) {
    audio.pause();
    audio.removeAttribute("src");
    audio.load();
    playGeneration++;
    index = -1;
  } else if (i < index) index--;
  queue.splice(i, 1);
  saveState();
  showQueue();
  updateNow();
}
async function playFromList(id) {
  failedPlaybackIDs.clear();
  radioSeed = "";
  queue = [...filtered()];
  index = queue.findIndex((x) => x.id === id);
  await playIndex(index);
}
async function playIndex(i, start = 0) {
  if (i < 0 || i >= queue.length) return;
  const gen = ++playGeneration;
  index = i;
  offset = 0;
  plannedNext = "";
  prefetched = "";
  lastPrefetchCheck = 0;
  audio.pause();
  audio.removeAttribute("src");
  audio.load();
  try {
    await api("playback", { id: queue[i].id });
    await api("cache/prefetch", { id: "" });
    if (gen !== playGeneration) return;
    audio.onloadedmetadata = () => {
      if (
        gen === playGeneration &&
        Number.isFinite(audio.duration) &&
        audio.duration > 0 &&
        !queue[i].duration
      ) {
        queue[i].duration = audio.duration;
        updateNow();
      }
      if (gen === playGeneration && start > 0) {
        try {
          audio.currentTime = start;
        } catch (e) {
          toast(tr("Не вдалося відновити позицію: ") + e.message);
        }
      }
    };
    audio.onerror=()=>handlePlaybackFailure(gen,tr("Помилка аудіопотоку або непідтримуваний формат"));
    audio.src = url(
      (start > 0 ? "/media?id=" : "/stream?id=") +
        encodeURIComponent(queue[i].id),
    );
    updateNow();
    render();
    saveState();
    await audio.play();
    api("player/played", { id: queue[i].id }).catch(() => {});
    if (gen === playGeneration) $("toggle").textContent = "❚❚";
  } catch (e) {
    if(gen===playGeneration && e.name!=="AbortError")handlePlaybackFailure(gen,e.message);
  }
  setMedia();
}
function updateNow() {
  updatePlayingRows();
  const t = queue[index];
  updateFavoriteButton($("favoriteNow"), t?.id);
  $("nowTitle").textContent = t?.title || tr("Обери свій настрій");
  $("nowArtist").textContent =
    t?.artist || tr("Додай музику та натисни відтворення");
  $("nowArt").hidden = !t;
  if (t) $("nowArt").src = artwork(t.thumbnail);
  $("duration").textContent = time(t?.duration);
  $("toggle").textContent = audio.paused ? "▶" : "❚❚";
  $("radioNow").classList.toggle("active", !!radioSeed);
}
async function next(manual = true) {
  if(manual)failedPlaybackIDs.clear();
  if (!queue.length) return;
  if (!manual && repeat === "one") {
    await playIndex(index);
    return;
  }
  let n = index + 1;
  if (plannedNext) {
    const plannedIndex = queue.findIndex((t) => t.id === plannedNext);
    if (plannedIndex >= 0) n = plannedIndex;
  } else if (shuffle && queue.length > 1) {
    do {
      n = Math.floor(Math.random() * queue.length);
    } while (n === index);
  }
  if (n >= queue.length) {
    if (radioSeed) {
      await appendRadio();
      n = index + 1;
    }
    if (
      n >= queue.length &&
      !radioSeed &&
      settings.recommendations &&
      repeat !== "all"
    ) {
      await appendRecommendations();
      n = index + 1;
    }
    if (n >= queue.length) {
      if (repeat === "all") n = 0;
      else {
        audio.pause();
        updateNow();
        return;
      }
    }
  }
  await playIndex(n);
}
async function resumePlayback(){
 if(index<0||!queue[index])return;
 if(audio.error || !audio.getAttribute("src") || handledErrorGeneration===playGeneration){failedPlaybackIDs.clear();return playIndex(index);}
 const gen=playGeneration;
 try{await audio.play();}catch(e){if(e.name!=="AbortError")handlePlaybackFailure(gen,e.message);}
}
async function toggle() {
  if (!queue.length) {
    if (filtered().length) {
      queue = [...filtered()];
      await playIndex(0);
    }
    return;
  }
  if (index < 0) {
    await playIndex(0);
    return;
  }
  if (!audio.src) {
    await playIndex(index, offset);
    return;
  }
  if (audio.paused) await resumePlayback();
  else audio.pause();
  updateNow();
}
async function search(fresh = false) {
  const q = $("query").value.trim();
  if (!q) return;
  const btn = $("searchForm").querySelector("button");
  btn.disabled = true;
  btn.textContent = tr("Пошук…");
  try {
    const result = await api("search", { query: q, refresh: fresh });
    view = "search";
    playlist = 0;
    searchQuery = q;
    visible = result.tracks;
    title(
      tr("Результати пошуку"),
      result.cached
        ? tr("З локального кешу • натисни ↻, щоб оновити")
        : tr("Знайдено на YouTube • назви й посилання збережено"),
    );
    await load();
    render();
  } finally {
    btn.disabled = false;
    btn.textContent = tr("Знайти");
  }
}
function textPrompt(caption, value = "") {
  return new Promise((resolve) => {
    $("textTitle").textContent = caption;
    $("textValue").value = value;
    const done = (v) => {
      $("textForm").onsubmit = null;
      $("textCancel").onclick = null;
      $("textDialog").onclose = null;
      $("textDialog").close();
      resolve(v);
    };
    $("textForm").onsubmit = (e) => {
      e.preventDefault();
      done($("textValue").value.trim());
    };
    $("textCancel").onclick = () => done(null);
    $("textDialog").onclose = () => done(null);
    $("textDialog").showModal();
    $("textValue").focus();
  });
}
async function newPlaylist() {
  const name = await textPrompt(tr("Новий плейлист"));
  if (!name) return null;
  const p = await api("playlist/create", { name });
  await load();
  return p.id;
}
function options(select, empty = false) {
  select.replaceChildren();
  if (empty) {
    const o = el("option", tr("Тільки в бібліотеку"));
    o.value = "0";
    select.append(o);
  }
  for (const p of lists.filter((x) => !x.system)) {
    const o = el("option", p.name);
    o.value = p.id;
    select.append(o);
  }
}
async function pick(ids) {
  pickIDs = ids;
  if (!ids.length) return;
  if (!lists.some((p) => !p.system)) {
    const id = await newPlaylist();
    if (!id) return;
  }
  options($("pickPlaylist"));
  $("picker").showModal();
}
async function watchJob(job) {
  $("notice").hidden = false;
  $("notice").textContent = tr("Імпорт триває…");
  while (true) {
    await new Promise((r) => setTimeout(r, 1200));
    const j = await api("job?id=" + job.id);
    if (j.status === "running") continue;
    if (j.status === "error") {
      $("notice").textContent = j.message;
      throw Error(j.message);
    }
    await load();
    return j;
  }
}
async function importTracks(target, pid) {
  const job = await api("import", { query: target, playlist: Number(pid) });
  const j = await watchJob(job);
  $("notice").textContent = j.message;
  toast(j.message);
  if (pid) await openPlaylist(Number(pid));
  else await showLibrary();
}
async function radio(t) {
  if (radioBusy) return;
  radioBusy = true;
  try {
    const job = await api("radio", { id: t.id });
    const j = await watchJob(job);
    const ids = JSON.parse(j.message);
    const tracks = ids
      .map((id) => library.find((x) => x.id === id))
      .filter(Boolean);
    queue = [t, ...tracks.filter((x) => x.id !== t.id)];
    radioSeed = t.id;
    repeat = "off";
    $("repeat").classList.remove("active");
    $("notice").textContent =
      tr("Радіо за треком «") + t.title + "» • " + queue.length + tr(" треків");
    showQueue();
    await playIndex(0);
  } finally {
    radioBusy = false;
  }
}
async function appendRadio() {
  if (radioBusy) return;
  radioBusy = true;
  try {
    const seed = queue[index]?.id || radioSeed;
    const j = await watchJob(await api("radio", { id: seed }));
    const ids = JSON.parse(j.message),
      known = new Set(queue.map((t) => t.id));
    const added = ids
      .filter((id) => !known.has(id))
      .map((id) => library.find((x) => x.id === id))
      .filter(Boolean);
    queue.push(...added);
    if (!added.length) toast(tr("YouTube не повернув нових треків радіо"));
    saveState();
    if (view === "queue") showQueue();
  } finally {
    radioBusy = false;
  }
}
function saveState() {
  if (saveTimer) return;
  saveTimer = setTimeout(() => {
    saveTimer = null;
    api("state", {
      value: {
        ids: queue.map((t) => t.id),
        index,
        position: offset + (audio.currentTime || 0),
        volume: audio.volume,
        repeat,
        shuffle,
        radioSeed,
      },
    }).catch(() => {});
  }, 350);
}
async function authState() {
  settings = await api("settings");
  loadExperienceSettings();
  $("browser").value = ["chrome", "edge", "brave"].includes(settings.browser)
    ? settings.browser
    : "edge";
  $("authState").textContent = settings.cookies
    ? tr("YouTube Music підключено")
    : tr("YouTube Music не підключено");
  $("cacheEnabled").checked = !!settings.cacheEnabled;
  $("cacheDir").value = settings.cacheDir || "";
  $("offlineOnly").checked = !!settings.offlineOnly;
  $("earlyBuffering").checked = settings.earlyBuffering !== false;
  await refreshPairUI();
  loadExperienceSettings();
}
function setMedia() {
  const t = queue[index];
  if (!t || !("mediaSession" in navigator)) return;
  navigator.mediaSession.metadata = new MediaMetadata({
    title: t.title,
    artist: t.artist,
    artwork: artwork(t.thumbnail) ? [{ src: artwork(t.thumbnail) }] : [],
  });
  for (const [name, fn] of Object.entries({
    play: () => resumePlayback(),
    pause: () => audio.pause(),
    nexttrack: () => next(),
    previoustrack: () => playIndex(Math.max(0, index - 1)),
    seekto: (d) => playIndex(index, d.seekTime),
  })) {
    try {
      navigator.mediaSession.setActionHandler(name, safely(fn));
    } catch {}
  }
}
$("favoritesNav").onclick = safely(async () => { await load(); $("filter").value = ""; showFavorites(); });
$("playlistsNav").onclick = safely(async () => { await load(); $("filter").value = ""; showPlaylists(); });
$("favoriteNow").onclick = safely(() => setFavorite(queue[index]?.id));
$("home").onclick = safely(showLibrary);
$("liked").onclick = safely(() => openPlaylist(1));
$("queueNav").onclick = showQueue;
$("filter").oninput = render;
$("searchForm").onsubmit = safely(async (e) => {
  e.preventDefault();
  await search();
});
$("refresh").onclick = safely(refreshView);
$("newPlaylist").onclick = safely(async () => {
  const id = await newPlaylist();
  if (id) await openPlaylist(id);
});
$("renamePlaylist").onclick = safely(async () => {
  const name = await textPrompt(
    tr("Перейменувати плейлист"),
    lists.find((x) => x.id === playlist)?.name,
  );
  if (name) {
    await api("playlist/rename", { playlist, name });
    await refreshView();
  }
});
$("deletePlaylist").onclick = safely(async () => {
  if (
    confirm(tr("Видалити цей локальний плейлист? Треки залишаться в бібліотеці."))
  ) {
    await api("playlist/delete", { playlist });
    await load();
    await showLibrary();
  }
});
$("playAll").onclick = safely(async () => {
  if (!filtered().length) return;
  radioSeed = "";
  queue = [...filtered()];
  await playIndex(0);
});
$("shuffleAll").onclick = safely(async () => {
  queue = [...filtered()];
  if (!queue.length) return;
  for (let i = queue.length - 1; i > 0; i--) {
    let j = Math.floor(Math.random() * (i + 1));
    [queue[i], queue[j]] = [queue[j], queue[i]];
  }
  radioSeed = "";
  await playIndex(0);
});
$("addAll").onclick = safely(() => pick(filtered().map((x) => x.id)));
$("confirmPick").onclick = safely(async () => {
  await api("playlist/add", {
    playlist: Number($("pickPlaylist").value),
    ids: pickIDs,
  });
  $("picker").close();
  await load();
  if (view === "playlist") await openPlaylist(playlist);
  toast(tr("Додано у плейлист"));
});
$("pickNew").onclick = safely(async () => {
  const id = await newPlaylist();
  if (id) {
    options($("pickPlaylist"));
    $("pickPlaylist").value = id;
  }
});
$("importBtn").onclick = () => {
  options($("importPlaylist"), true);
  if (view === "playlist" && !systemPlaylist(playlist))
    $("importPlaylist").value = playlist;
  $("importURL").value = $("query").value.startsWith("http")
    ? $("query").value
    : "";
  $("importDialog").showModal();
};
$("doImport").onclick = safely(async () => {
  const target = $("importURL").value.trim(),
    pid = $("importPlaylist").value;
  if (!target) return;
  $("importDialog").close();
  await importTracks(target, pid);
});
$("toggle").onclick = safely(toggle);
$("next").onclick = safely(() => next());
$("prev").onclick = safely(() => playIndex(Math.max(0, index - 1)));
$("shuffle").onclick = () => {
  shuffle = !shuffle;
  plannedNext = "";
  prefetched = "";
  api("cache/prefetch", { id: "" }).catch(() => {});
  $("shuffle").classList.toggle("active", shuffle);
  saveState();
};
$("repeat").onclick = () => {
  plannedNext = "";
  prefetched = "";
  api("cache/prefetch", { id: "" }).catch(() => {});
  repeat = repeat === "off" ? "all" : repeat === "all" ? "one" : "off";
  $("repeat").textContent = repeat === "one" ? "↻¹" : "↻";
  $("repeat").classList.toggle("active", repeat !== "off");
  $("repeat").title =
    tr("Повтор: ") + { off: tr("вимкнено"), all: tr("черга"), one: tr("трек") }[repeat];
  saveState();
};
$("radioNow").onclick = safely(() => {
  if (queue[index]) return radio(queue[index]);
});
$("volume").oninput = () => {
  audio.volume = Number($("volume").value);
  saveState();
};
$("seek").onchange = safely(async () => {
  if (index >= 0) {
    const target = Number($("seek").value);
    if (
      audio.seekable.length &&
      target <= audio.seekable.end(audio.seekable.length - 1)
    ) {
      audio.currentTime = target;
      offset = 0;
      saveState();
    } else await playIndex(index, target);
  }
});
audio.ontimeupdate = () => {
  if(audio.currentTime>=5)failedPlaybackIDs.clear();
  const t = queue[index];
  $("elapsed").textContent = time(offset + audio.currentTime);
  $("seek").max = t?.duration || 0;
  $("seek").value = offset + audio.currentTime;
  saveState();
  schedulePrefetch().catch(() => {});
};
audio.onplay = audio.onpause = updateNow;
const failedPlaybackIDs=new Set();
let handledErrorGeneration=-1;
function handlePlaybackFailure(gen,reason){
 if(gen!==playGeneration || handledErrorGeneration===gen || !queue[index])return;
 handledErrorGeneration=gen;
 const failed=queue[index];failedPlaybackIDs.add(failed.id);
 api("player/error",{id:failed.id,reason:String(reason||tr("Не вдалося відтворити аудіо")).slice(0,1000)}).catch(()=>{});
 let target=-1;
 for(let step=1;step<queue.length;step++){const candidate=(index+step)%queue.length;if(!failedPlaybackIDs.has(queue[candidate].id)){target=candidate;break;}}
 if(target<0){audio.pause();updateNow();toast(tr("Не вдалося відтворити пісні в черзі. Відтворення зупинено."));return;}
 toast(tr("Не вдалося відтворити «")+failed.title+tr("». Перехід до наступної пісні…"));
 setTimeout(()=>{if(gen===playGeneration)playIndex(target).catch(()=>{});},250);
}
audio.onended = safely(() => {failedPlaybackIDs.clear();return next(false);});
audio.onerror=()=>handlePlaybackFailure(playGeneration,tr("Помилка аудіопотоку або непідтримуваний формат"));
audio.onwaiting = () => {
  $("nowArtist").textContent = tr("Буферизація…");
};
audio.onplaying = () => { updateNow(); schedulePrefetch().catch(() => {}); };
$("settingsBtn").onclick = safely(async () => {
  await authState();
  settingsGroup("main");
  $("settings").showModal();
});
$("saveCache").onclick = safely(async () => {
  settings = await api("settings", {
    cacheEnabled: $("cacheEnabled").checked,
    cacheDir: $("cacheDir").value,
    offlineOnly: $("offlineOnly").checked,
    earlyBuffering: $("earlyBuffering").checked,
  });
  plannedNext = "";
  prefetched = "";
  await api("cache/prefetch", { id: "" });
  render();
  updateNow();
  toast(tr("Налаштування кешування збережено"));
});
$("chooseCacheDir").onclick = safely(async () => {
  const r = await api("cache/folder", {});
  if (r.path) $("cacheDir").value = r.path;
});
$("cacheAll").onclick = safely(() => cacheTracks(filtered().map((t) => t.id)));
$("offlineNav").onclick = showOffline;
$("syncLikes").onclick = safely(async () => {
  const cfg = await api("settings");
  if (!cfg.cookies && !cfg.spotifyConnected) throw Error("Спочатку увійди в акаунт");
  $("syncLikes").disabled = true;
  $("likesSyncState").textContent = tr("Синхронізація…");
  try {
    const tasks = [];
    if (cfg.cookies) tasks.push(importTracks("https://music.youtube.com/playlist?list=LM", 1));
    if (cfg.spotifyConnected) tasks.push(api("spotify/sync", {}));
    const results = await Promise.allSettled(tasks);
    const errors = results.filter(r => r.status === "rejected").map(r => r.reason.message);
    $("likesSyncState").textContent = errors.length ? errors.join("; ") : tr("Синхронізацію запущено");
  } finally { $("syncLikes").disabled = false; }
});
function renderDiagnostics(status) {
  $("diagnostics").replaceChildren();
  for (const name of ["yt-dlp", "ffmpeg", "deno"]) {
    const available = status[name] && !["Не встановлено", "Не знайдено"].some(x => status[name].startsWith(x));
    const row = el("div", undefined, "component-item");
    const mark = el("span", available ? "✓" : "×", available ? "component-ready" : "component-missing");
    mark.setAttribute("aria-label", available ? tr("Встановлено") : tr("Не встановлено"));
    row.append(el("span", name), mark);
    $("diagnostics").append(row);
  }
}
$("checkTools").onclick = safely(async () => {
  $("checkTools").disabled = true;
  try { renderDiagnostics(await api("diagnostics")); }
  finally { $("checkTools").disabled = false; }
});
$("exit").onclick = safely(async () => {
  await api("state", {
    value: {
      ids: queue.map((t) => t.id),
      index,
      position: offset + (audio.currentTime || 0),
      duration:
        queue[index]?.duration ||
        (Number.isFinite(audio.duration) ? audio.duration : 0),
      volume: audio.volume,
      repeat,
      shuffle,
      radioSeed,
    },
  });
  audio.pause();
  await api("shutdown", {});
  document.body.replaceChildren(
    el("p", tr("Say Music Music завершено. Вікно можна закрити.")),
  );
  window.close();
});
document.addEventListener(
  "keydown",
  safely(async (e) => {
    if (
      ["INPUT", "SELECT", "TEXTAREA", "BUTTON"].includes(e.target.tagName) ||
      document.querySelector("dialog[open]")
    )
      return;
    if (e.code === "Space") {
      e.preventDefault();
      await toggle();
    }
    if (e.ctrlKey && e.code === "ArrowRight") {
      e.preventDefault();
      await next();
    }
    if (e.ctrlKey && e.code === "ArrowLeft") {
      e.preventDefault();
      await playIndex(Math.max(0, index - 1));
    }
  }),
);
safely(async () => {
  await load();
  settings = await api("settings");
  loadExperienceSettings();
  const s = await api("state");
  queue = (s.ids || [])
    .map((id) => library.find((t) => t.id === id))
    .filter(Boolean);
  index = Number.isInteger(s.index) ? Math.min(s.index, queue.length - 1) : -1;
  offset = s.position || 0;
  audio.volume =
    typeof s.volume === "number" ? Math.min(1, Math.max(0, s.volume)) : 0.7;
  $("volume").value = audio.volume;
  repeat = s.repeat || "off";
  shuffle = !!s.shuffle;
  radioSeed = s.radioSeed || "";
  $("shuffle").classList.toggle("active", shuffle);
  $("repeat").classList.toggle("active", repeat !== "off");
  $("repeat").textContent = repeat === "one" ? "↻¹" : "↻";
  await showLibrary();
  updateNow();
  if (index >= 0) {
    $("elapsed").textContent = time(offset);
    $("seek").max = queue[index].duration || 0;
    $("seek").value = offset;
  }
  const d = await api("diagnostics");
  renderDiagnostics(d);
  if (Object.values(d).some((v) => v === "Не встановлено" || v.startsWith("Не знайдено"))) {
    $("notice").hidden = false;
    $("notice").textContent =
      tr("Для відтворення потрібні компоненти. Відкрий Налаштування → Додатково → Компоненти.");
  }
})();

setInterval(() => api("heartbeat").catch(() => {}), 15000);

function settingsGroup(key) {
 document.querySelectorAll("[data-save-page]").forEach(b=>b.hidden=b.dataset.savePage!==key);
  if (key === "advanced") loadRecovery().catch(e=>toast(e.message));
  document
    .querySelectorAll("[data-settings-page]")
    .forEach((n) => (n.hidden = n.dataset.settingsPage !== key));
  document
    .querySelectorAll("[data-settings]")
    .forEach((n) => n.classList.toggle("active", n.dataset.settings === key));
}
document
  .querySelectorAll("[data-settings]")
  .forEach((b) => (b.onclick = () => settingsGroup(b.dataset.settings)));
settingsGroup("main");
let islandBackgroundImage = "";
function loadExperienceSettings() {
 $("language").value=settings.language||"uk";
  applyAppearance();
  $("interfaceColor").value = settings.interfaceColor || "#191b23";
  $("interfaceTheme").value = settings.interfaceTheme || "dark";
  $("interfaceAccent").value = settings.interfaceAccent || "#ae96ff";
  const c = settings.island || {};
  $("islandEdgeFree").checked=!!c.edgeFree;
 $("islandEdgeLength").value=c.edgeLength||90;
 $("islandEdgeThickness").value=c.edgeThickness||10;
 $("islandEdgeReaction").checked=c.edgeReaction!==false;
 $("islandEdgeReactionRadius").value=c.edgeReactionRadius||150;
  $("islandEdgeStandard").checked=c.edgeStandard!==false;
  $("islandEdgeColor").value=c.edgeColor||"#ae96ff";
  $("islandEdgeColor").disabled=$("islandEdgeStandard").checked;
  $("islandBackgroundColor").value = c.backgroundColor || "#18191d";
  islandBackgroundImage = c.backgroundImage || "";
  $("islandBackgroundStatus").textContent = islandBackgroundImage
    ? tr("Власний фон завантажено")
    : tr("Власний фон не вибрано");
  for (const key of [
    "Layout",
    "Theme",
    "Material",
    "Position",
    "Width",
    "Cover",
    "Radius",
    "Opacity",
    "OffsetX",
    "OffsetY",
  ])
    $("island" + key).value = c[key[0].toLowerCase() + key.slice(1)] ?? "";
  for (const key of ["Topmost", "Hover", "Animations"])
    $("island" + key).checked = !!c[key[0].toLowerCase() + key.slice(1)];
  $("islandEnabled").checked = !!c.enabled;
  $("closeToTray").checked = !!c.closeToTray;
  $("earlyBuffering").checked = settings.earlyBuffering !== false;
  $("recommendationsEnabled").checked = !!settings.recommendations;
  $("recommendationsNav").hidden = !settings.recommendations;
}
async function saveExperience() {
  const c = {
    ...settings.island,
    edgeFree:$("islandEdgeFree").checked,
 edgeLength:Number($("islandEdgeLength").value),
 edgeThickness:Number($("islandEdgeThickness").value),
 edgeReaction:$("islandEdgeReaction").checked,
 edgeReactionRadius:Number($("islandEdgeReactionRadius").value),
    edgeStandard:$("islandEdgeStandard").checked,
    edgeColor:$("islandEdgeColor").value,
    backgroundColor: $("islandBackgroundColor").value,
    backgroundImage: islandBackgroundImage,
    enabled: $("islandEnabled").checked,
    closeToTray: $("closeToTray").checked,
  };
  for (const key of ["Layout", "Theme", "Material", "Position"])
    c[key[0].toLowerCase() + key.slice(1)] = $("island" + key).value;
  for (const key of [
    "Width",
    "Cover",
    "Radius",
    "Opacity",
    "OffsetX",
    "OffsetY",
  ])
    c[key[0].toLowerCase() + key.slice(1)] = Number($("island" + key).value);
  for (const key of ["Topmost", "Hover", "Animations"])
    c[key[0].toLowerCase() + key.slice(1)] = $("island" + key).checked;
  settings = await api("experience/settings", {
    island: c,
    recommendations: $("recommendationsEnabled").checked,
    earlyBuffering: $("earlyBuffering").checked,
  });
  loadExperienceSettings();
  $("islandStatus").textContent = tr("Налаштування збережено");
  toast(tr("Налаштування збережено"));
}
$("islandEdgeStandard").onchange=()=>{$("islandEdgeColor").disabled=$("islandEdgeStandard").checked;};
$("saveIsland").onclick = $("saveRecommendations").onclick =
  safely(saveExperience);
$("previewIsland").onclick = safely(async () => {
  await saveExperience();
  await api("desktop/action", { action: "island" });
});
$("minimizeTray").onclick = safely(async () => {
  await saveExperience();
  await api("desktop/action", { action: "tray" });
});
async function showArtists() {
  view = "artists";
  playlist = 0;
  visible = [];
  artistList = await api("artists");
  title(
    tr("Виконавці"),
    tr("Обери виконавця, щоб переглянути його пісні у твоїй бібліотеці"),
  );
  renderArtistCards();
}
function renderArtistCards() {
  const q = $("filter").value.toLocaleLowerCase(),
    items = artistList.filter((a) =>
      (a.name || tr("Невідомий виконавець")).toLocaleLowerCase().includes(q),
    );
  $("tracks").replaceChildren();
  $("empty").hidden = true;
  $("artistGrid").hidden = false;
  $("artistGrid").replaceChildren();
  $("count").textContent = items.length + tr(" виконавців");
  for (const a of items) {
    const b = button("", a.name, () => showArtist(a.name));
    b.className = "artist-card";
    const img = el("img");
    img.src = artwork(a.thumbnail) || "/placeholder.svg";
    img.alt = "";
    img.loading = "lazy";
    b.append(
      img,
      el("strong", a.name || tr("Невідомий виконавець")),
      el("span", a.count + tr(" пісень")),
    );
    $("artistGrid").append(b);
  }
}
async function showArtist(name) {
  selectedArtist = name;
  view = "artist";
  playlist = 0;
  visible = await api("artist/tracks?name=" + encodeURIComponent(name));
  $("filter").value = "";
  title(name || tr("Невідомий виконавець"), tr("Пісні виконавця у твоїй бібліотеці"));
  render();
}
$("artistsNav").onclick = safely(showArtists);
async function showRecommendations(refresh = false) {
  if (!settings.recommendations) {
    await authState();
    settingsGroup("main");
  $("settings").showModal();
    settingsGroup("main");
    return;
  }
  view = "recommendations";
  playlist = 0;
  title(tr("Нові й старі улюблені пісні"), tr("Добірка Say Music · отримання пісень…"));
  const r = await api("recommendations", { refresh });
  if (view !== "recommendations") return;
  visible = r.tracks;
  $("subtitle").textContent = r.message;
  render();
  await load();
}
$("recommendationsNav").onclick = safely(() => showRecommendations());
$("openRecommendations").onclick = safely(async () => {
  await saveExperience();
  $("settings").close();
  await showRecommendations();
});
async function appendRecommendations() {
  if (recommendationBusy) return;
  recommendationBusy = true;
  try {
    const r = await api("recommendations", { seed: queue[index]?.id });
    const ids = new Set(queue.map((t) => t.id));
    queue.push(...r.tracks.filter((t) => !ids.has(t.id)));
    saveState();
    if (view === "queue") showQueue();
  } finally {
    recommendationBusy = false;
  }
}
setInterval(async () => {
  if (experiencePolling) return;
  experiencePolling = true;
  try {
    const commands = await api("player/tick", {
      id: queue[index]?.id || "",
      playing: !audio.paused,
      position: offset + (audio.currentTime || 0),
      duration:
        queue[index]?.duration ||
        (Number.isFinite(audio.duration) ? audio.duration : 0),
      volume: audio.volume,
      after: commandAfter,
    });
    for (const c of commands) {
      commandAfter = Math.max(commandAfter, c.seq);
      switch (c.action) {
        case "toggle":
          await toggle();
          break;
        case "next":
          await next();
          break;
        case "previous":
          await playIndex(Math.max(0, index - 1));
          break;
        case "seek":
          if (index >= 0) {
            if (
              audio.seekable.length &&
              c.value <= audio.seekable.end(audio.seekable.length - 1)
            ) {
              audio.currentTime = c.value;
              offset = 0;
            } else await playIndex(index, c.value);
          }
          break;
        case "volume":
          audio.volume = Math.min(1, Math.max(0, c.value));
          $("volume").value = audio.volume;
          break;
      }
    }
  } catch {
  } finally {
    experiencePolling = false;
  }
}, 500);

function showOffline() {
  view = "offline";
  playlist = 0;
  visible = library.filter((t) => t.cached);
  title(tr("Офлайн-треки"), tr("Аудіофайли збережено на цьому ПК"));
  render();
}
async function cacheTracks(ids) {
  if (!ids.length) return;
  const job = await api("cache/add", { ids });
  $("notice").hidden = false;
  while (true) {
    const j = await api("job?id=" + job.id);
    $("notice").textContent = j.message;
    if (j.status !== "running") {
      if (j.status === "error") throw Error(j.message);
      break;
    }
    await new Promise((r) => setTimeout(r, 1000));
  }
  await load();
  if (view === "offline") showOffline();
  else if (view === "playlist") await openPlaylist(playlist);
  else {
    const map = new Map(library.map((t) => [t.id, t]));
    visible = visible.map((t) => map.get(t.id) || t);
    render();
  }
  toast(tr("Збереження офлайн завершено"));
}
async function schedulePrefetch() {
  if (
    index < 0 ||
    audio.paused ||
    settings.offlineOnly ||
    settings.earlyBuffering === false ||
    prefetchBusy ||
    repeat === "one"
  )
    return;
  if (Date.now() - lastPrefetchCheck < 2500) return;
  lastPrefetchCheck = Date.now();
  if (radioSeed && index >= queue.length - 1 && !radioBusy) {
    await appendRadio();
  }
  if (!plannedNext) {
    let n = index + 1;
    if (shuffle && queue.length > 1) {
      do {
        n = Math.floor(Math.random() * queue.length);
      } while (n === index);
    } else if (n >= queue.length && repeat === "all") n = 0;
    if (n >= queue.length) return;
    plannedNext = queue[n].id;
  }
  if (prefetched === plannedNext) return;
  prefetchBusy = true;
  try {
    const r = await api("cache/prefetch", { id: plannedNext });
    if (r.scheduled) prefetched = plannedNext;
  } finally {
    prefetchBusy = false;
  }
}
setInterval(async () => {
  if (cachePolling) return;
  cachePolling = true;
  try {
    const r = await api("cache/status");
    const cached = new Set(r.cached);
    let changed = false;
    for (const list of [library, visible, queue])
      for (const t of list) {
        const value = cached.has(t.id);
        if (!!t.cached !== value) {
          t.cached = value;
          changed = true;
        }
      }
    if (changed) {
      if (view === "offline") showOffline();
      else render();
    }
  } catch {
  } finally {
    cachePolling = false;
  }
}, 5000);

let integrationEvent = 0,
  integrationJob = "",
  integrationPolling = false;
setInterval(async () => {
  if (integrationPolling) return;
  integrationPolling = true;
  try {
    const s = await api("integration/status");
    if (s.spotify) $("spotifyState").textContent = !s.spotify.direct ? tr("Spotify не підключено") : s.spotify.message || (s.spotify.connected ? tr("Spotify підключено") : tr("Spotify не підключено"));
    $("cancelSpotifySync").hidden=s.spotify?.status!=="running";
    const stamp = s.job
      ? s.job.id + ":" + s.job.status + ":" + s.job.count
      : "";
    const changed = integrationEvent !== s.event || integrationJob !== stamp;
    integrationEvent = s.event;
    integrationJob = stamp;
    if (s.job) {
      $("notice").hidden = false;
      $("notice").textContent = s.job.message;
    }
    if (changed) {
      await load();
      if (view === "library") await showLibrary();
      else if (view === "playlist") await openPlaylist(playlist);
      else if (view === "offline") showOffline();
      else if (view === "favorites") showFavorites();
      else if (view === "playlists") showPlaylists();
      if ($("settings").open) {
        $("authState").textContent = s.authenticated
          ? tr("YouTube Music підключено")
          : tr("YouTube Music не підключено");
      }
    }
  } catch {
  } finally {
    integrationPolling = false;
  }
}, 2500);

// Account and browser setup stays inside the application; no shell commands are shown.
let setupPolling = false,
  testExtensionSince = 0;
async function openSetup(step) {
  return api("setup/open", { step, browser: $("browser").value });
}
async function refreshSetup() {
  const s = await api(
    "setup/status?browser=" + encodeURIComponent($("browser").value),
  );
  $("extensionPath").value = s.extensionDir;
  $("bridgeRegistration").textContent = s.directReady
    ? tr("Активний прямий канал. Реєстрація додаткового мосту не потрібна.")
    : s.registered
      ? tr("Зв’язок зареєстровано для обраного браузера.")
      : tr("Встав сесію у головних налаштуваннях або зареєструй додатковий міст цією кнопкою.");
  $("removeBridge").disabled = !s.registered;
  const seenAt = Date.parse(s.seenAt);
  $("bridgeConnection").textContent =
    !s.registered && !s.directReady
      ? tr("Спочатку встав сесію або встанови додатковий міст.")
      : s.seen && (!testExtensionSince || seenAt >= testExtensionSince)
        ? tr("✓ Зв’язок із розширенням підтверджено: ") +
          new Date(seenAt).toLocaleTimeString()
        : testExtensionSince
          ? tr("Очікування розширення… Перевір відкриту вкладку YouTube Music і доступ розширення до сайту.")
          : tr("У цьому запуску ще не було зв’язку з розширенням.");
  if (s.job) {
    $("componentState").textContent = s.job.status === "running" ? tr("Встановлення…") : s.job.status === "done" ? tr("Готово") : tr("Не вдалося встановити компоненти");
    if (s.job.status === "done" && !$("installTools").disabled) return;
    if (s.job.status === "done") renderDiagnostics(await api("diagnostics"));
    $("installTools").disabled = s.job.status === "running";
    if (s.job.status === "done") $("notice").hidden = true;
  }
}
$("extensionSetup").onclick = safely(async () => {
  await refreshSetup();
  $("extensionWizard").showModal();
});
$("installBridge").onclick = safely(async () => {
  try {
    await api("setup/bridge", {});
    await refreshSetup();
  } catch (e) {
    $("bridgeRegistration").textContent = e.message;
    throw e;
  }
});
$("openExtensions").onclick = safely(() => openSetup("extensions"));
$("removeBridge").onclick = safely(async () => {
  await api("setup/unbridge", {});
  testExtensionSince = 0;
  await refreshSetup();
});
$("showExtensionFolder").onclick = safely(() => api("setup/folder", {}));
$("copyExtensionPath").onclick = safely(async () => {
  const input = $("extensionPath");
  try {
    await navigator.clipboard.writeText(input.value);
    $("copyExtensionPath").textContent = tr("✓ Скопійовано");
  } catch {
    input.focus();
    input.select();
    if (!document.execCommand("copy"))
      throw Error(tr("Шлях виділено — натисни Ctrl+C"));
  }
});
$("testExtension").onclick = safely(async () => {
  testExtensionSince = Date.now();
  await openSetup("youtube-test");
  await refreshSetup();
});
$("installTools").onclick = safely(async () => {
  try {
    await api("setup/components", {});
    await refreshSetup();
  } catch (e) {
    $("componentState").textContent = e.message;
    throw e;
  }
});
async function refreshPairUI() {
  const state = await api("pair/status");
  $("pairState").textContent =
    state.error ||
    (state.paired
      ? state.listening
        ? tr("Розширення підключено")
        : tr("Зв’язок недоступний")
      : tr("Розширення не підключено"));
  return state;
}
async function importSession() {
  $("importSession").disabled = true;
  try {
    await api("pair/import", { text: $("sessionText").value });
    $("sessionText").value = "";
    $("sessionDialog").close();
    await authState();
    toast(
      tr("Сесію підключено"),
    );
  } catch (e) {
    $("sessionImportState").textContent = e.message;
  } finally {
    $("importSession").disabled = false;
  }
}
$("pasteSession").onclick = safely(async () => {
  $("sessionImportState").textContent = "";
  $("sessionText").value = "";
  $("sessionDialog").showModal();
  try {
    const text = await navigator.clipboard.readText();
    if (text.trim().startsWith("SAYMUSIC_SESSION_V1")) {
      $("sessionText").value = text;
      await importSession();
      return;
    }
  } catch {}
  $("sessionText").focus();
  $("sessionImportState").textContent =
    tr("Встав скопійовану сесію через Ctrl+V, потім натисни «Підключити».");
});
$("sessionDialog").addEventListener("close", () => {
  $("sessionText").value = "";
});
$("importSession").onclick = safely(importSession);
$("retryPair").onclick = safely(async () => {
  await api("pair/retry", {});
  await refreshPairUI();
});
setInterval(async () => {
  if (setupPolling || (!$("settings").open && !$("extensionWizard").open))
    return;
  setupPolling = true;
  try {
    await refreshSetup();
  } catch (e) {
    $("componentState").textContent = e.message;
  } finally {
    setupPolling = false;
  }
}, 2000);

$("islandClearBackground").onclick = () => {
  islandBackgroundImage = "";
  $("islandBackgroundFile").value = "";
  $("islandBackgroundStatus").textContent = tr("Фон видалено. Збережи вигляд.");
};
$("islandBackgroundFile").onchange = async () => {
  const file = $("islandBackgroundFile").files[0];
  if (!file) return;
  if (file.size > 20 * 1024 * 1024) {
    toast(tr("Зображення завелике (максимум 20 МБ)"));
    return;
  }
  const url = URL.createObjectURL(file);
  try {
    const img = new Image();
    await new Promise((resolve, reject) => {
      img.onload = resolve;
      img.onerror = reject;
      img.src = url;
    });
    const scale = Math.min(1, 1024 / Math.max(img.width, img.height));
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(img.width * scale));
    canvas.height = Math.max(1, Math.round(img.height * scale));
    canvas.getContext("2d").drawImage(img, 0, 0, canvas.width, canvas.height);
    islandBackgroundImage = canvas.toDataURL("image/jpeg", 0.85);
    $("islandMaterial").value = "custom";
    $("islandBackgroundStatus").textContent =
      tr("Картинку підготовлено. Збережи вигляд.");
  } catch {
    toast(tr("Не вдалося відкрити зображення"));
  } finally {
    URL.revokeObjectURL(url);
  }
};

let favoritePolling = false;
setInterval(async () => {
  if (favoritePolling) return;
  favoritePolling = true;
  try {
    const next = await api("favorites");
    const changed = JSON.stringify(next.map(t => t.id)) !== JSON.stringify(favorites.map(t => t.id));
    favorites = next;
    favoriteIDs = new Set(next.map(t => t.id));
    updateFavoriteButton($("favoriteNow"), queue[index]?.id);
    if (changed) {
      if (view === "favorites") showFavorites();
      else render();
    }
  } catch {} finally { favoritePolling = false; }
}, 1500);

const systemTheme = window.matchMedia ? window.matchMedia("(prefers-color-scheme: dark)") : null;
function applyAppearance() {
 window.SayI18n?.set(settings.language || "uk");
  const mode = settings.interfaceTheme || "dark";
  const light = mode === "light" || (mode === "system" && systemTheme && !systemTheme.matches);
  const color = /^#[0-9a-f]{6}$/i.test(settings.interfaceColor || "") ? settings.interfaceColor : "#191b23";
  const rgb = [1,3,5].map(i => parseInt(color.slice(i,i+2),16));
  const blend = (values,target,amount) => "#" + values.map(v => Math.round(v*(1-amount)+target*amount).toString(16).padStart(2,"0")).join("");
  const mix = (target, amount) => blend(rgb,target,amount);
  const darkRGB = rgb.map(v => Math.round(v * Math.min(1,72/Math.max(1,...rgb))));
  const darkMix = (target, amount) => blend(darkRGB,target,amount);
  const root = document.documentElement;
  root.dataset.theme = light ? "light" : "dark";
  root.style.colorScheme = light ? "light" : "dark";
  const palette = light ? {
    bg:mix(255,.96), panel:mix(255,.91), soft:mix(255,.85), sidebar:mix(255,.93),
    input:mix(255,.98), hover:mix(255,.79), selected:mix(255,.81), border:mix(255,.72),
    text:"#20212a", muted:"#606373", accent:mix(0,.22), "on-accent":"#ffffff"
  } : {
    bg:darkMix(0,.38), panel:darkMix(0,0), soft:darkMix(255,.055), sidebar:darkMix(0,.16),
    input:darkMix(0,.20), hover:darkMix(255,.12), selected:darkMix(255,.10), border:darkMix(255,.10),
    text:"#eeeef6", muted:"#a4a7b8", accent:color.toLowerCase() === "#191b23" ? "#ae96ff" : mix(255,.64), "on-accent":"#211638"
  };
  const accent = /^#[0-9a-f]{6}$/i.test(settings.interfaceAccent || "") ? settings.interfaceAccent : "#ae96ff";
  palette.accent = accent;
  const channels = [1,3,5].map(i => parseInt(accent.slice(i,i+2),16)/255).map(v => v <= .04045 ? v/12.92 : ((v+.055)/1.055)**2.4);
  const luminance = channels[0]*.2126+channels[1]*.7152+channels[2]*.0722;
  palette["on-accent"] = luminance > .179 ? "#14141c" : "#ffffff";
  root.style.setProperty("--accent-rgb", [1,3,5].map(i => parseInt(accent.slice(i,i+2),16)).join(","));
  for (const [key,value] of Object.entries(palette)) root.style.setProperty("--"+key, value);
}
if (systemTheme) systemTheme.addEventListener("change", () => applyAppearance());
$("saveAppearance").onclick = safely(async () => {
  settings = await api("appearance/settings", {
    interfaceColor: $("interfaceColor").value,
    interfaceAccent: $("interfaceAccent").value,
    interfaceTheme: $("interfaceTheme").value
  });
  applyAppearance();
  $("appearanceStatus").textContent = tr("Вигляд збережено");
});
$("resetAppearance").onclick = safely(async () => {
  settings = await api("appearance/settings", {interfaceColor:"#191b23", interfaceTheme:"dark", interfaceAccent:"#ae96ff"});
  loadExperienceSettings();
  $("appearanceStatus").textContent = tr("Стандартний вигляд відновлено");
});

$("spotifyLiked").onclick = safely(async () => {
 await load(); const p=lists.find(p => p.system && p.source === "spotify");
 if (p) await openPlaylist(p.id);
});
$("cancelSpotifySync").onclick=safely(async()=>{await api("spotify/cancel",{});$("notice").textContent=tr("Spotify: зупинка синхронізації…");});
let recoveryKind="trash", recoveryLoading=false;
async function loadRecovery() {
 if(recoveryLoading)return;
 recoveryLoading=true;
 try {
  const items=await api("recovery/list?kind="+recoveryKind);
  $("recoveryTrash").classList.toggle("active",recoveryKind==="trash");
  $("recoveryIssues").classList.toggle("active",recoveryKind!=="trash");
  $("emptyTrash").hidden=recoveryKind!=="trash";
  $("recoveryList").replaceChildren();
  $("recoveryStatus").textContent=items.length ? tr("Пісень: ")+items.length : recoveryKind==="trash" ? tr("Кошик порожній") : tr("Проблемних пісень немає");
  for(const item of items) {
   const row=el("div",undefined,"recovery-row"),info=el("div",undefined,"recovery-info"),actions=el("div",undefined,"actions");
   const t=recoveryKind==="trash" ? item.track : item;
   info.append(el("strong",t.title),el("span",t.artist || ""));
   if(recoveryKind==="trash") {
    info.append(el("small",new Date(item.deleted*1000).toLocaleString()));
    actions.append(button(tr("Відновити"),tr("Повернути пісню, уподобання та плейлисти"),async()=>{
     await api("recovery/restore",{id:t.id});await refreshView();await loadRecovery();toast(tr("Пісню відновлено"));
    }),button(tr("Видалити назавжди"),tr("Видалити запис із кошика"),async()=>{
     if(!confirm(tr("Видалити пісню з кошика назавжди?")))return;
     await api("recovery/purge",{id:t.id});await loadRecovery();
    }));
   } else {
    const names={playback_error:tr("Помилка відтворення"),not_found:tr("Не знайдено аудіо"),unavailable:tr("Недоступна"),download_error:tr("Помилка завантаження"),import_gap:tr("Пропущена позиція"),source_unavailable:tr("Недоступна у джерелі"),import_error:tr("Помилка імпорту")};
    info.append(el("small",names[item.kind] || tr("Проблема з піснею")),el("p",item.reason,"hint"));
    actions.append(button(item.trackId ? tr("Повторити пошук") : tr("Повторити синхронізацію"),tr("Спробувати отримати пісню ще раз"),async()=>{
     const result=await api("recovery/retry",{id:item.id});
     toast(result.sync ? tr("Залиш вкладку Spotify відкритою до завершення синхронізації") : tr("Повторна спроба запущена"));
     await loadRecovery();
    }),button(tr("Прибрати запис"),tr("Прибрати зі списку проблемних"),async()=>{await api("recovery/dismiss",{id:item.id});await loadRecovery();}));
   }
   row.append(info,actions);$("recoveryList").append(row);
  }
 }finally{recoveryLoading=false;}
}
$("recoveryTrash").onclick=safely(async()=>{recoveryKind="trash";await loadRecovery();});
$("recoveryIssues").onclick=safely(async()=>{recoveryKind="issues";await loadRecovery();});
$("recoveryRefresh").onclick=safely(loadRecovery);
$("emptyTrash").onclick=safely(async()=>{
 if(!confirm(tr("Назавжди видалити всі пісні з кошика?")))return;
 await api("recovery/empty",{});await loadRecovery();
});
setInterval(()=>{
 if($("settings").open && !document.querySelector('[data-settings-page="advanced"]').hidden)loadRecovery().catch(()=>{});
},3000);

$("language").onchange=safely(async()=>{
 const before=settings.language||"uk";
 try {
 settings=await api("appearance/settings",{interfaceColor:settings.interfaceColor||"#191b23",interfaceAccent:settings.interfaceAccent||"#ae96ff",interfaceTheme:settings.interfaceTheme||"dark",language:$("language").value});
 applyAppearance();await refreshView();updateNow();
 $("repeat").title=tr("Повтор: ")+{off:tr("вимкнено"),all:tr("черга"),one:tr("трек")}[repeat];
 toast(tr("Налаштування збережено"));
 }catch(e){$("language").value=before;throw e;}
});

const embeddedPolls = {};
for (const [provider,button,label] of [["youtube","webviewYouTube","webviewYouTubeStatus"],["spotify","webviewSpotify","webviewSpotifyStatus"]]) {
 $(button).onclick=safely(async()=>{
  await api("embedded-auth/start?provider="+provider,{});
  $(label).textContent=tr("Очікування входу у вікні WebView2…");
  if(embeddedPolls[provider])clearInterval(embeddedPolls[provider]);
  embeddedPolls[provider]=setInterval(async()=>{
   try {
    const s=await api("embedded-auth/status?provider="+provider);$(label).textContent=s.message;
    if(s.status!=="waiting"){clearInterval(embeddedPolls[provider]);delete embeddedPolls[provider];if(s.status==="connected")await authState();}
   }catch(e){$(label).textContent=e.message;}
  },1500);
 });
}
