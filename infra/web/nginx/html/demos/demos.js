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
      case 'joined': return { cls: 'ok', text: 'joined a match', why: 'same game as ' + (res.linked_to || []).map(clean).join(', ') + (res.anchors ? ' (' + res.anchors + ' shared events)' : '') };
      case 'already_stored': return { cls: 'ok', text: 'already here', why: who + "'s demo was already uploaded; it is now yours too" };
      default: return { cls: 'new', text: 'new match', why: res.reason || 'no other demo of this game yet' };
    }
  }

  // Sends files one at a time; calls onRow(row) as each changes.  A row is {name, cls, text, why, matchId}.
  async function uploadAll(files, onRow) {
    for (const f of files) {
      const row = { name: f.name, cls: 'sending', text: 'sending', why: (f.size / 1048576).toFixed(1) + ' MB', matchId: '' };
      onRow(row);
      if (!EXT.test(f.name)) { Object.assign(row, { cls: 'error', text: 'skipped', why: 'only raw .dm_57 / .dm_59 / .dm_60 demo files are accepted (not zips)' }); onRow(row); continue; }
      try {
        const res = await api('POST', '/upload?name=' + encodeURIComponent(f.name), f);
        Object.assign(row, describe(res), { matchId: res.demo.match_id });
      } catch (e) { Object.assign(row, { cls: 'error', text: 'error', why: e.message }); }
      onRow(row);
    }
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

  return { esc, clean, clock, getKey, setKey, api, describe, uploadAll, dropZone, when, mode };
})();
