"use strict";
const tr = (s) => window.SayI18n ? window.SayI18n.t(s) : s;
const $ = (id) => document.getElementById(id),
  token = new URLSearchParams(location.search).get("token");
const capsuleMode = new URLSearchParams(location.search).has("capsule");
history.replaceState(null, "", "/island.html");
let currentTrackID = "", currentFavorite = false, favoriteBusy = false;
let cfg = {},
  pinned = false,
  seeking = false,
  busy = false,
  stamp = "";
const api = async (path, data) => {
  const r = await fetch(
    "/api/" + path + "?token=" + encodeURIComponent(token),
    {
      method: data === undefined ? "GET" : "POST",
      headers: data === undefined ? {} : { "Content-Type": "application/json" },
      body: data === undefined ? undefined : JSON.stringify(data),
    },
  );
  const v = await r.json();
  if (!r.ok) throw Error(v.error || tr("Помилка"));
  return v;
};
const time = (n) =>
  Math.floor(Math.max(0, n || 0) / 60) +
  ":" +
  String(Math.floor(Math.max(0, n || 0) % 60)).padStart(2, "0");
async function run(path, data) {
  try {
    await api(path, data);
    $("error").hidden = true;
  } catch (e) {
    $("error").textContent = e.message;
    $("error").hidden = false;
    setTimeout(() => ($("error").hidden = true), 4000);
  }
}
function transportIcon(id,playing=false){
 const paths={previous:'<path d="M6 5v14M18 5 8 12l10 7Z"/>',next:'<path d="M18 5v14M6 5l10 7-10 7Z"/>',toggle:playing?'<path d="M8 5v14M16 5v14"/>':'<path d="m8 5 11 7-11 7Z"/>'};
 $(id).innerHTML='<svg viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">'+paths[id]+'</svg>';
}
for(const id of ["previous","toggle","next"])transportIcon(id);
for (const action of ["previous", "toggle", "next"])
  $(action).onclick = () => run("player/command", { action });
$("seek").onpointerdown = () => {
  seeking = true;
};
document.addEventListener("pointerup", () => {
  seeking = false;
});
$("seek").onchange = () => {
  seeking = false;
  run("player/command", { action: "seek", value: Number($("seek").value) });
};
$("favorite").onclick = async () => {
  if (!currentTrackID || favoriteBusy) return;
  favoriteBusy = true;
  $("favorite").disabled = true;
  await run("favorites/set", {id: currentTrackID, favorite: !currentFavorite});
  favoriteBusy = false;
  await poll();
};
$("restore").onclick = () => run("desktop/action", { action: "restore" });
$("hide").onclick = () => run("desktop/action", { action: "hide-island" });
$("pin").onclick = () => {
  pinned = !pinned;
  $("pin").textContent = pinned ? "◆" : "◇";
  if (!capsuleMode) document.body.classList.remove("collapsed");
  run("desktop/action", { action: pinned ? "pin" : "unpin" });
  run("desktop/action", { action: "expand" });
};
let hoverTimer, collapseTimer;
let sideExpanded = false;
if (capsuleMode) {
  document.body.classList.add("capsule");
  const base = document.createElement("div");
  base.className = "capsule-base";
  const content = document.querySelector(".content");
  content.insertBefore(base, content.firstChild);
  base.append(document.querySelector(".artwork"), $("info"), $("ambient"));
}

function applySideLayout() {
  const controls = $("previous").parentElement;
  const inline = capsuleMode && sideExpanded && cfg.layout === "side";
  document.body.classList.toggle("side-expanded", inline);
  if (inline) {
    const content = document.querySelector(".content");
    if (controls.parentElement !== content)
      content.insertBefore(controls, content.firstChild);
  } else if (controls.parentElement !== $("info"))
    $("info").appendChild(controls);
}
window.setSideExpanded = (expanded) => {
  sideExpanded = expanded;
  applySideLayout();
};
window.setIslandExpanded = (expanded) => {
  if (!expanded) {
    pinned = false;
    $("pin").textContent = "◇";
  }
  const wasCollapsed = document.body.classList.contains("collapsed");
  document.body.classList.toggle("collapsed", capsuleMode || !expanded);
  if (
    expanded &&
    wasCollapsed &&
    !capsuleMode &&
    cfg.animations &&
    $("island").animate
  ) {
    for (const animation of $("island").getAnimations()) animation.cancel();
    $("island").animate(
      [
        { opacity: 0, transform: "translateY(12px) scale(.96)" },
        { opacity: 1, transform: "translateY(0) scale(1)" },
      ],
      { duration: 240, easing: "cubic-bezier(.16,1,.3,1)" },
    );
  }
};
function expand() {
  clearTimeout(collapseTimer);
  window.setIslandExpanded(true);
  run("desktop/action", { action: "expand" });
}
document.getElementById("island").addEventListener("click", (event) => {
  if (
    document.body.classList.contains("collapsed") &&
    !event.target.closest("button")
  )
    expand();
});
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape") {
    pinned = false;
    $("pin").textContent = "◇";
    run("desktop/action", { action: "unpin" });
    run("desktop/action", { action: "collapse-force" });
  }
});
document.documentElement.addEventListener("mouseenter", () => {
  clearTimeout(collapseTimer);
  if (cfg.hover && document.body.classList.contains("collapsed"))
    hoverTimer = setTimeout(expand, 180);
});
document.documentElement.addEventListener("mouseleave", () => {
  clearTimeout(hoverTimer);
  if (!pinned)
    collapseTimer = setTimeout(() => {
      run("desktop/action", { action: "collapse" });
    }, 550);
});
let artworkGeneration = 0,
  pendingArtwork = "";
function changeArtwork(src) {
  if ($("art").getAttribute("src") === src || pendingArtwork === src) return;
  pendingArtwork = src;
  const generation = ++artworkGeneration;
  const next = new Image();
  next.onload = () => {
    if (generation !== artworkGeneration) return;
    pendingArtwork = "";
    $("artPrevious").src = $("art").src;
    $("art").src = src;
    $("ambient").src = src;
    if (cfg.animations && $("artPrevious").animate) {
      $("artPrevious").animate(
        [
          { opacity: 1, transform: "scale(1)" },
          { opacity: 0, transform: "scale(1.035)" },
        ],
        { duration: 320, easing: "ease-out" },
      );
      $("art").animate(
        [
          { opacity: 0, transform: "scale(.97)" },
          { opacity: 1, transform: "scale(1)" },
        ],
        { duration: 320, easing: "ease-out" },
      );
    }
  };
  next.onerror = () => {
    if (generation === artworkGeneration) {
      pendingArtwork = "";
      $("art").src = "/placeholder.svg";
    }
  };
  next.src = src;
}
async function poll() {
  if (busy) return;
  busy = true;
  try {
    const s = await api("player/status");
    window.SayI18n?.set(s.language || "uk");
    cfg = s.island;
    if (cfg.edgeFree && capsuleMode) {
      const light=s.interfaceTheme==="light" || (s.interfaceTheme==="system" && window.matchMedia && window.matchMedia("(prefers-color-scheme: light)").matches);
      cfg={...cfg,theme:light?"light":"dark",material:"solid",backgroundColor:light?"#f0f1f5":"#1a1b21"};
    }
    if(cfg.edgeFree && cfg.layout==="side")cfg={...cfg,layout:"compact"};
    const p = s.player,
      t = p.track || {};
    currentTrackID = t.id || "";
    currentFavorite = !!s.favorite;
    $("favorite").disabled = !currentTrackID || favoriteBusy;
    $("favorite").textContent = currentFavorite ? "♥" : "♡";
    $("favorite").classList.toggle("active", currentFavorite);
    $("favorite").setAttribute("aria-pressed", String(currentFavorite));
    $("favorite").title = currentFavorite ? tr("Прибрати з уподобаних") : tr("Додати до уподобаних");
    $("title").textContent = t.title || "Say Music";
    $("artist").textContent = t.artist || tr("Обери пісню у програмі");
    transportIcon("toggle",p.playing);
    $("elapsed").textContent = time(p.position);
    $("duration").textContent = time(t.duration);
    if (!seeking) {
      $("seek").max = t.duration || 1;
      $("seek").value = p.position || 0;
    }
    const art = s.offline
      ? "/placeholder.svg"
      : t.thumbnail || "/placeholder.svg";
    changeArtwork(art);
    document.documentElement.style.setProperty(
      "--nav-art",
      "url(" + JSON.stringify(art) + ")",
    );
    const next = JSON.stringify([cfg, s.interfaceColor]);
    if (next !== stamp) {
      stamp = next;
      const collapsed =
        document.body.classList.contains("collapsed") && !pinned;
      document.body.className = [
        capsuleMode ? "capsule" : "",
        cfg.layout,
        cfg.theme,
        cfg.material,
        cfg.animations ? "" : "no-motion",
        collapsed ? "collapsed" : "",
        sideExpanded && cfg.layout === "side" ? "side-expanded" : "",
      ].join(" ");
      document.documentElement.style.setProperty("--radius", cfg.radius + "px");
      document.documentElement.style.setProperty("--cover", cfg.cover + "px");
      document.documentElement.style.setProperty(
        "--tint",
        (100 - cfg.opacity) / 200 + 0.12,
      );
      applySideLayout();
      const rgb = cfg.theme === "light" ? "239,240,244" : "24,25,29";
      document.documentElement.style.setProperty(
        "--solid",
        cfg.backgroundColor || "#18191d",
      );
      document.documentElement.style.setProperty("--surface-rgb", rgb);
      const tint = s.interfaceColor || "#191b23";
      const tintRGB = [1, 3, 5]
        .map((i) => parseInt(tint.slice(i, i + 2), 16))
        .join(",");
      document.documentElement.style.setProperty("--interface-rgb", tintRGB);
      for (const key of ["--fg", "--muted", "--border"])
        document.body.style.removeProperty(key);
      if (
        cfg.material === "acrylic" ||
        (capsuleMode &&
          cfg.material !== "borderless" &&
          cfg.material !== "transparent")
      ) {
        const [red, green, blue] = tintRGB.split(",").map(Number);
        const light = red * 0.2126 + green * 0.7152 + blue * 0.0722 > 160;
        document.body.style.setProperty("--fg", light ? "#14151a" : "#f6f6fa");
        document.body.style.setProperty(
          "--muted",
          light ? "#5f6169" : "#bbbbc3",
        );
        document.body.style.setProperty(
          "--border",
          light ? "rgba(0,0,0,.2)" : "rgba(255,255,255,.18)",
        );
      }

      const custom = cfg.backgroundImage
        ? "/api/island/background?token=" +
          encodeURIComponent(token) +
          "&v=" +
          encodeURIComponent(cfg.backgroundImage)
        : "/placeholder.svg";
      document.documentElement.style.setProperty(
        "--nav-custom",
        "url(" + JSON.stringify(custom) + ")",
      );
      if ($("customBackground").getAttribute("src") !== custom)
        $("customBackground").src = custom;
      document.documentElement.style.setProperty(
        "--bg",
        "rgba(" + rgb + "," + 1 + ")",
      );
    }
  } catch {
  } finally {
    busy = false;
  }
}
setInterval(poll, 700);
poll();

window.animateEdgePreview=()=>{
 const el=document.getElementById("island");
 if(!cfg.animations || !el.animate)return;
 const side=cfg.edgeSide||"right";
 const translate={left:"translateX(-18px)",right:"translateX(18px)",top:"translateY(-18px)",bottom:"translateY(18px)"}[side];
 for(const animation of el.getAnimations())animation.cancel();
 el.animate([{opacity:0,transform:translate},{opacity:1,transform:"translate(0,0)"}],{duration:180,easing:"ease-out"});
};
