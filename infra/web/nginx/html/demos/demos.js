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

  const when = iso => { const d = new Date(iso); return isNaN(d) ? '' : d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' }); };
  const mode = m => m.gametype === 6 ? 'Stopwatch' : '';

  // what a match is called: its name if it has one, else the map
  const title = m => (m.name || '').trim() || m.map;
  return { title, esc, clean, clock, getKey, setKey, api, describe, uploadAll, renderSent, dropZone, when, mode };
})();
