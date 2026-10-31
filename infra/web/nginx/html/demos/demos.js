'use strict';
// Shared by the demo pages: escaping, clocks, the uploader key, and uploading.
const D = (() => {
  const esc = s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const clean = s => String(s || '').replace(/\^[0-9A-Za-z]/g, '');
  const clock = ms => { const s = Math.max(0, Math.floor(ms / 1000)); return Math.floor(s / 60) + ':' + String(s % 60).padStart(2, '0'); };
  const EXT = /\.dm_(57|59|60|61)$/i;
  const KEY = 'userdemo-key';

  // The key is kept for this browser tab only.
  const getKey = () => { try { return sessionStorage.getItem(KEY) || ''; } catch (e) { return ''; } };
  const setKey = v => { try { sessionStorage.setItem(KEY, v); } catch (e) { /* private mode */ } };

  async function api(method, path, body) {
    const headers = {};
    if (getKey()) headers['X-Upload-Key'] = getKey();
    const r = await fetch('/api/demos' + path, { method, body, headers });
    let j = null;
    try { j = await r.json(); } catch (e) { /* not json */ }
    if (r.status === 404 && !(j && j.error)) throw new Error('Demo matches are not available on this server.');
    if (!r.ok) throw new Error((j && j.error) || ('HTTP ' + r.status));
    return j;
  }

  // One line per upload: what became of the file.
  function describe(res) {
    const d = res.demo || {};
    const who = clean(d.recorder) || (d.spectator ? 'a spectator' : 'a player');
    switch (res.status) {
      case 'split': {
        const g = res.games || [], joined = g.filter(x => x.status === 'joined').length;
        return { cls: 'ok', text: 'split into ' + g.length + ' games', why: 'it spans several games, so each is in its own match' + (joined ? ' (' + joined + ' joined demos already here)' : '') + (res.series_id ? '; the games are linked as a series' : '') };
      }
      case 'joined': return { cls: 'ok', text: 'joined a match', why: 'same game as ' + (res.linked_to || []).map(clean).join(', ') + (res.anchors ? ' (' + res.anchors + ' shared events)' : '') };
      case 'already_stored': return { cls: 'ok', text: 'already here', why: who + "'s demo was already uploaded; it is now yours too" };
      default: return { cls: 'new', text: 'new match', why: res.reason || 'no other demo of this game yet' };
    }
  }

  // POST of a file with upload progress (fetch cannot report it); onProgress(sent, total).
  function post(path, file, onProgress) {
    return new Promise((resolve, reject) => {
      const x = new XMLHttpRequest();
      x.open('POST', '/api/demos' + path);
      if (getKey()) x.setRequestHeader('X-Upload-Key', getKey());
      x.upload.onprogress = e => { if (e.lengthComputable) onProgress(e.loaded, e.total); };
      x.onerror = () => reject(new Error('The upload failed (network error).'));
      x.onload = () => {
        let j = null;
        try { j = JSON.parse(x.responseText); } catch (e) { /* not json */ }
        if (x.status === 404 && !(j && j.error)) return reject(new Error('Demo matches are not available on this server.'));
        if (x.status < 200 || x.status > 299) return reject(new Error((j && j.error) || ('HTTP ' + x.status)));
        resolve(j);
      };
      x.send(file);
    });
  }

  let batchNo = 0, queue = [], running = false;
  // Lists every dropped file at once (waiting), then sends them one at a time.  Files dropped while others are still going join the
  // same queue and the same overall bar.  Each row is {name, size, batch, sent, cls, text, why, matchId}, pushed to rows; onChange()
  // after any change.  The returned promise resolves when the queue has run dry, for the call that started it; for the calls that only
  // added to a running queue it resolves at once.
  async function uploadAll(files, rows, onChange) {
    if (!running) batchNo++;
    for (const f of files) {
      const row = { name: f.name, size: f.size, batch: batchNo, sent: 0, cls: 'wait', text: 'waiting', why: (f.size / 1048576).toFixed(1) + ' MB', matchId: '' };
      rows.push(row);
      queue.push({ f, row });
    }
    onChange();
    if (running) return;
    running = true;
    try {
      while (queue.length) {
        const { f, row } = queue.shift();
        await sendOne(f, row, onChange);
      }
    } finally { running = false; }
  }

  async function sendOne(f, row, onChange) {
    if (!EXT.test(f.name)) { Object.assign(row, { cls: 'error', text: 'skipped', why: 'only raw .dm_57 / .dm_59 / .dm_60 demo files are accepted (not zips)', done: true }); onChange(); return; }
    Object.assign(row, { cls: 'sending', text: 'sending' }); onChange();
    try {
      const res = await post('/upload?name=' + encodeURIComponent(f.name), f, (n, total) => {
        row.sent = n / total;
        if (n >= total) Object.assign(row, { text: 'analysing', why: 'lining it up with the other demos' });
        onChange();
      });
      Object.assign(row, describe(res), { sent: 1, matchId: res.demo.match_id, done: true });
    } catch (e) { Object.assign(row, { cls: 'error', text: 'error', why: e.message, done: true }); }
    onChange();
  }

  // Draws the rows into a table (with a bar each) and the latest batch's overall bar into
  // `overall`.  extra(row) gives the last column.
  function renderSent(table, overall, rows, extra) {
    table.style.display = rows.length ? '' : 'none';
    // an upload is "analysing" once all its bytes are out, which is not yet done
    const frac = r => r.done || r.cls === 'error' ? 1 : r.cls === 'wait' ? 0 : Math.min(r.sent, 0.95);
    table.querySelector('tbody').innerHTML = rows.map(r => {
      const pct = Math.round(frac(r) * 100);
      return '<tr><td>' + esc(r.name) + '</td><td><span class="tag ' + r.cls + '">' + esc(r.text) + '</span> <span class="why">' + esc(r.why) + '</span>' +
        (r.cls === 'wait' || r.cls === 'error' ? '' : '<div class="pbar' + (r.done ? ' done' : '') + '"><i style="width:' + pct + '%"></i></div>') +
        '</td><td>' + (extra ? extra(r) : '') + '</td></tr>';
    }).join('');
    const cur = rows.filter(r => r.batch === batchNo);
    const live = cur.length > 1 || cur.some(r => !r.done);
    overall.style.display = cur.length && live ? '' : 'none';
    if (!cur.length) return;
    const total = cur.reduce((n, r) => n + (r.size || 1), 0);
    const got = cur.reduce((n, r) => n + frac(r) * (r.size || 1), 0);
    const finished = cur.filter(r => r.done || r.cls === 'error').length;
    overall.querySelector('i').style.width = Math.round(got / total * 100) + '%';
    overall.querySelector('span').textContent = finished + ' of ' + cur.length + ' demos done' + (finished === cur.length ? '' : ' · ' + Math.round(got / total * 100) + '%');
  }

  // Wires a drop zone and its file input.
  function dropZone(zone, input, handle) {
    zone.onclick = () => input.click();
    zone.onkeydown = e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); input.click(); } };
    input.onchange = e => { handle([...e.target.files]); e.target.value = ''; };
    ['dragenter', 'dragover'].forEach(ev => zone.addEventListener(ev, e => { e.preventDefault(); zone.classList.add('over'); }));
    ['dragleave', 'drop'].forEach(ev => zone.addEventListener(ev, e => { e.preventDefault(); zone.classList.remove('over'); }));
    zone.addEventListener('drop', e => handle([...e.dataTransfer.files]));
  }

  // The site's header and footer, the same as on the main pages (there is no "Demos" in them: the tool is
  // reached from a blurb on the home page).  The pages carry a plain one too, for when scripts are off.
  const ICONS = `<div class="icon-link-group">
    <a data-umami-event="click-discord" href="https://discord.gg/yJkWaYn" class="icon-link" target="_blank" rel="noopener" title="S4NDMoD on Discord">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20.317 4.369A19.79 19.79 0 0 0 15.885 3c-.211.375-.444.879-.608 1.279a18.27 18.27 0 0 0-5.487 0A12.6 12.6 0 0 0 9.182 3a19.74 19.74 0 0 0-4.435 1.37C1.578 9.046.9 13.58 1.24 18.057a19.9 19.9 0 0 0 5.993 3.03c.483-.658.913-1.357 1.284-2.093a12.87 12.87 0 0 1-2.023-.97c.17-.124.336-.253.497-.386 3.902 1.804 8.13 1.804 11.986 0 .163.133.329.262.497.386-.641.383-1.323.71-2.025.971.372.735.8 1.435 1.284 2.092a19.85 19.85 0 0 0 5.998-3.029c.4-5.192-.983-9.687-3.414-13.688ZM8.984 15.29c-1.174 0-2.14-1.062-2.14-2.366 0-1.305.944-2.368 2.14-2.368 1.204 0 2.163 1.072 2.14 2.368 0 1.304-.944 2.366-2.14 2.366Zm6.032 0c-1.174 0-2.14-1.062-2.14-2.366 0-1.305.944-2.368 2.14-2.368 1.204 0 2.163 1.072 2.14 2.368 0 1.304-.936 2.366-2.14 2.366Z"/></svg>
    </a>
    <a data-umami-event="click-github-source" href="https://github.com/S4NDM4NN/s4ndmod26" class="icon-link" target="_blank" rel="noopener" title="S4NDMoD on GitHub">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 .5C5.73.5.5 5.73.5 12c0 5.08 3.29 9.39 7.86 10.91.58.1.79-.25.79-.56 0-.28-.01-1.02-.02-2-3.2.7-3.88-1.54-3.88-1.54-.52-1.34-1.28-1.7-1.28-1.7-1.04-.72.08-.7.08-.7 1.15.08 1.76 1.19 1.76 1.19 1.03 1.76 2.7 1.25 3.36.96.1-.75.4-1.25.73-1.54-2.55-.29-5.24-1.28-5.24-5.68 0-1.26.45-2.29 1.19-3.09-.12-.29-.52-1.46.11-3.05 0 0 .97-.31 3.18 1.18a11.1 11.1 0 0 1 5.79 0c2.2-1.49 3.17-1.18 3.17-1.18.63 1.59.24 2.76.11 3.05.74.8 1.19 1.83 1.19 3.09 0 4.41-2.69 5.38-5.25 5.67.41.36.78 1.08.78 2.17 0 1.56-.01 2.82-.01 3.2 0 .31.21.67.8.56C20.21 21.39 23.5 17.08 23.5 12 23.5 5.73 18.27.5 12 .5Z"/></svg>
    </a>
    <a data-umami-event="click-facebook" href="https://www.facebook.com/S4NDMoD/" class="icon-link" target="_blank" rel="noopener" title="S4NDMoD on Facebook">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M22 12.06C22 6.5 17.52 2 12 2S2 6.5 2 12.06c0 5.02 3.66 9.18 8.44 9.94v-7.03H7.9v-2.91h2.54V9.85c0-2.5 1.49-3.89 3.77-3.89 1.09 0 2.24.2 2.24.2v2.46h-1.26c-1.24 0-1.63.77-1.63 1.56v1.88h2.78l-.44 2.91h-2.34V22c4.78-.76 8.44-4.92 8.44-9.94Z"/></svg>
    </a>
  </div>`;
  function chrome() {
    const h = document.querySelector('header.s4-header'), f = document.querySelector('footer.s4-foot');
    if (h) h.innerHTML = '<a class="brand" href="/"><img src="/img/s4nd-logo.svg" alt="S4NDMoD26" width="135" height="48"><div><h1>S4NDMoD<span>26</span></h1><small>A Return to Castle Wolfenstein mod</small></div></a>' +
      '<nav class="s4-nav" aria-label="Site"><a href="/">Play</a><a class="live" href="/paths.html?live=1">\u25cf Live</a><a href="/stats.html">Stats</a><a href="/contact.html">Contact</a></nav>' + ICONS;
    if (f) {
      f.innerHTML = '<div class="foot-top"><nav aria-label="Footer"><a href="/">Play</a><a href="/paths.html?live=1">Live</a><a href="/stats.html">Stats</a><a href="/contact.html">Contact</a>' +
        '<a href="https://discord.gg/yJkWaYn" target="_blank" rel="noopener">Discord</a><a href="https://github.com/S4NDM4NN/s4ndmod26" target="_blank" rel="noopener">GitHub</a></nav><span class="build" id="build-version"></span></div>' +
        '<div class="fine">S4NDMoD26 is a Return to Castle Wolfenstein mod. Not affiliated with id Software or Activision.</div>';
      fetch('/version.txt').then(r => r.ok ? r.text() : '').then(v => { const el = document.getElementById('build-version'); if (el && v.trim()) el.textContent = 'build ' + v.trim(); }).catch(() => {});
    }
  }
  chrome();

  const when = iso => { const d = new Date(iso); return isNaN(d) ? '' : d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' }); };
  const mode = m => m.gametype === 6 ? 'Stopwatch' : '';

  // what a match is called: its name if it has one, else the map
  const title = m => (m.name || '').trim() || m.map;
  return { title, esc, clean, clock, getKey, setKey, api, describe, uploadAll, renderSent, dropZone, when, mode };
})();
