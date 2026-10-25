"use strict";
(() => {
 let timer,busy=false,lastRun=0;
 const buttons=()=>[...document.querySelectorAll('button[data-video-id],button[data-spotify-id]')].filter(b=>{
  const r=b.getBoundingClientRect();return b.isConnected && r.bottom>=0 && r.top<=innerHeight && r.right>=0 && r.left<=innerWidth;
 });
 async function refresh(force=false){
  if(busy || (!force && Date.now()-lastRun<1000))return;
  const visible=buttons();if(!visible.length)return;
  busy=true;lastRun=Date.now();
  try{
   for(const provider of ['youtube','spotify']){
    const attribute=provider==='youtube'?'videoId':'spotifyId';
    const ids=[...new Set(visible.map(b=>b.dataset[attribute]).filter(Boolean))];
    for(let i=0;i<ids.length;i+=200){
     const batch=ids.slice(i,i+200);
     const result=await chrome.runtime.sendMessage({type:'CHECK_LIBRARY',provider,ids:batch});
     if(!result?.ok || result.running===false || !Array.isArray(result.existing))continue;
     const found=new Set(result.existing),requested=new Set(batch);
     for(const b of visible){const id=b.dataset[attribute];if(!b.isConnected || !requested.has(id))continue;
      const saved=found.has(id);b.dataset.inLibrary=String(saved);
      b.classList.toggle('say-music-saved',saved);
      if(!b.disabled && b.textContent!==(saved?'✓':'＋'))b.textContent=saved?'✓':'＋';
      b.title=saved?'Уже в бібліотеці Say Music':'Додати трек у Say Music';
      b.setAttribute('aria-label',b.title);
     }
    }
   }
  }catch{}finally{busy=false;}
 }
 function schedule(){clearTimeout(timer);timer=setTimeout(()=>refresh(),Math.max(300,1000-(Date.now()-lastRun)));}
 window.SayMembership={refresh:()=>refresh(true)};
 new MutationObserver(schedule).observe(document.documentElement,{childList:true,subtree:true,attributes:true,attributeFilter:['data-video-id','data-spotify-id']});
 addEventListener('scroll',schedule,true);setInterval(()=>refresh(),5000);schedule();
})();
