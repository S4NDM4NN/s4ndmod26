// Watch the play of the game in a modal over this page.  /play/ needs SharedArrayBuffer, which a
// browser only grants to a cross-origin isolated page, and an iframe is only isolated if the page
// embedding it is too.  This page is served with the same COOP/COEP headers as /play/ (nginx.conf),
// so the iframe qualifies; if isolation is somehow unavailable, fall back to a popup window.
function openPotgPlayer(name, player) {
  // with a player number this is that player's highlight rather than the play of the game
  const isHighlight = player !== undefined && player !== null && player !== '';
  const url = '/play/?replay=' + encodeURIComponent(name) + (isHighlight ? '&player=' + encodeURIComponent(player) : '');
  if (!window.crossOriginIsolated) {
    const w = window.open(url, 'potg_' + name, 'popup=yes,width=1100,height=680');
    if (!w) alert('Allow pop-ups for this site to watch the play of the game.');
    return;
  }
  if (document.getElementById('potg-modal')) return;

  if (!document.getElementById('potg-modal-style')) {
    const st = document.createElement('style');
    st.id = 'potg-modal-style';
    st.textContent =
      '#potg-modal{position:fixed;inset:0;z-index:1000;background:rgba(0,0,0,.78);display:flex;align-items:center;justify-content:center}' +
      '#potg-modal .potg-box{width:min(96vw,1200px);height:min(90vh,760px);background:#09090b;border:1px solid #6a3800;border-radius:6px;display:flex;flex-direction:column;overflow:hidden;box-shadow:0 8px 40px rgba(0,0,0,.7)}' +
      '#potg-modal .potg-bar{display:flex;align-items:center;justify-content:space-between;padding:6px 12px;background:#18181b;color:#fbbf24;font:600 13px system-ui,sans-serif;flex:none}' +
      '#potg-modal .potg-bar a{color:#a1a1aa;font-weight:400;font-size:12px;margin-right:14px;text-decoration:none}' +
      '#potg-modal .potg-bar a:hover{color:#fff}' +
      '#potg-modal .potg-bar a svg{display:none;vertical-align:middle}' +
      '#potg-modal .potg-bar button{background:none;border:0;color:#a1a1aa;font-size:18px;line-height:1;cursor:pointer;padding:2px 6px}' +
      '#potg-modal .potg-bar button:hover{color:#fff}' +
      '#potg-modal iframe{flex:1;width:100%;border:0;background:#000}' +
      '@media (max-width:700px),(max-height:500px){#potg-modal .potg-box{width:100vw;height:100vh;height:100dvh;border:0;border-radius:0}' +
      '#potg-modal .potg-bar{padding:0 4px 0 12px}' +
      '#potg-modal .potg-bar a,#potg-modal .potg-bar button{display:inline-flex;align-items:center;justify-content:center;width:44px;height:44px;padding:0;margin:0}' +
      '#potg-modal .potg-bar a .txt{display:none}#potg-modal .potg-bar a svg{display:inline}' +
      '#potg-modal .potg-bar button{font-size:22px}}';
    document.head.appendChild(st);
  }

  const back = document.createElement('div');
  back.id = 'potg-modal';
  const box = document.createElement('div');
  box.className = 'potg-box';
  const bar = document.createElement('div');
  bar.className = 'potg-bar';
  const title = document.createElement('span');
  title.textContent = isHighlight ? '★ Highlight' : '★ Play of the Game';
  const right = document.createElement('span');
  const pop = document.createElement('a');
  pop.href = url; pop.target = '_blank'; pop.rel = 'noopener';
  pop.title = 'Open in a new window';
  pop.setAttribute('aria-label', 'Open in a new window');
  const popTxt = document.createElement('span');
  popTxt.className = 'txt';
  popTxt.textContent = 'open in new window';
  pop.append(popTxt);
  pop.insertAdjacentHTML('beforeend', '<svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M11 3h6v6M17 3l-8 8M14 11v5a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5"/></svg>');
  // Render the clip to a 1080p60 MP4 in this browser (the player page does the work; see wasm/shell.html)
  const exp = document.createElement('a');
  exp.href = url + '&export=1';
  exp.title = 'Render this clip to a 1080p MP4 video';
  exp.setAttribute('aria-label', 'Export as MP4');
  const expTxt = document.createElement('span');
  expTxt.className = 'txt';
  expTxt.textContent = 'export MP4';
  exp.append(expTxt);
  exp.insertAdjacentHTML('beforeend', '<svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10 3v10M6 9l4 4 4-4M3 17h14"/></svg>');
  exp.addEventListener('click', (e) => {
    e.preventDefault();
    title.textContent = '★ Exporting MP4';
    frame.src = url + '&export=1';
    exp.remove();
    pop.remove();
  });
  const close = document.createElement('button');
  close.type = 'button'; close.setAttribute('aria-label', 'Close'); close.textContent = '✕';
  right.append(exp, pop, close);
  bar.append(title, right);

  const frame = document.createElement('iframe');
  frame.allow = 'fullscreen; autoplay; gamepad; cross-origin-isolated';
  frame.src = url;
  frame.addEventListener('load', () => frame.focus());
  box.append(bar, frame);
  back.append(box);

  const onKey = (e) => { if (e.key === 'Escape') closeModal(); };
  function closeModal() {
    document.removeEventListener('keydown', onKey);
    frame.src = 'about:blank';      // tear the engine down
    back.remove();
  }
  close.addEventListener('click', closeModal);
  pop.addEventListener('click', closeModal);
  back.addEventListener('mousedown', (e) => { if (e.target === back) closeModal(); });
  document.addEventListener('keydown', onKey);
  document.body.append(back);
}
