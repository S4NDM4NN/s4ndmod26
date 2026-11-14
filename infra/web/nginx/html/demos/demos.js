'use strict';
// Shared by the demo pages: escaping, clocks, who is signed in, and uploading.  Adding or changing demos needs a site
// account that holds the demo_upload permission; the pages only send the session cookie.
const D = (() => {
  const esc = s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const clean = s => String(s || '').replace(/\^[0-9A-Za-z]/g, '');
  const clock = ms => { const s = Math.max(0, Math.floor(ms / 1000)); return Math.floor(s / 60) + ':' + String(s % 60).padStart(2, '0'); };
  const EXT = /\.dm_(57|59|60|61)$/i;

  // Who is signed in on the site, and whether they may add demos.  `why` says what is missing, for a page to show.
  let accessP = null;
  function access(fresh) {
    if (fresh || !accessP) {
      accessP = fetch('/api/account', { credentials: 'same-origin' }).then(r => r.ok ? r.json() : null).catch(() => null).then(me => {
        const can = !!(me && me.email_verified && !me.suspended && me.permissions.includes('demo_upload'));
        const why = can ? '' : !me ? 'signin' : !me.email_verified ? 'confirm' : 'upgrade';
        return { me, can, why };
      });
    }
    return accessP;
  }

  async function api(method, path, body) {
    const r = await fetch('/api/demos' + path, { method, body, credentials: 'same-origin' });
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

  // Where the signed-in key stands against the hourly upload limit: from /mine and from every upload's answer
  // (refusals too), so a page can say how many uploads are left, or why one was refused.
  let rate = null;
  function setRate(r) { rate = r ? Object.assign({ at: Date.now() }, r) : null; }
  const waitText = secs => secs < 90 ? Math.max(secs, 1) + ' seconds' : secs < 5400 ? Math.round(secs / 60) + ' minutes' : 'an hour';
  // seconds until the next slot opens, counting down from when the numbers were fetched
  const resetIn = () => rate ? Math.max(rate.reset_secs - Math.round((Date.now() - rate.at) / 1000), 0) : 0;
  function rateText() {
    if (!rate) return '';
    // the slots that opened since the numbers were fetched are not counted back: refetching (or the next upload) does that
    const shared = rate.by === 'ip' ? ' (this limit is shared by everyone on this address)' : '';
    if (rate.remaining === 0) return 'Upload limit reached: ' + rate.used + ' of ' + rate.limit + ' used this hour. The next upload opens in ' + waitText(resetIn()) + '.' + shared;
    return 'Uploads this hour: ' + rate.used + ' of ' + rate.limit + ' used, ' + rate.remaining + ' left' + shared + '.';
  }

  // POST of a file with upload progress (fetch cannot report it); onProgress(sent, total).
  function post(path, file, onProgress) {
    return new Promise((resolve, reject) => {
      const x = new XMLHttpRequest();
      x.open('POST', '/api/demos' + path);
      x.upload.onprogress = e => { if (e.lengthComputable) onProgress(e.loaded, e.total); };
      x.onerror = () => reject(new Error('The upload failed (network error).'));
      x.onload = () => {
        let j = null;
        try { j = JSON.parse(x.responseText); } catch (e) { /* not json */ }
        if (j && j.rate) setRate(j.rate);
        const fail = msg => { const e = new Error(msg); e.status = x.status; reject(e); };
        if (x.status === 404 && !(j && j.error)) return fail('Demo matches are not available on this server.');
        if (x.status < 200 || x.status > 299) return fail((j && j.error) || ('HTTP ' + x.status));
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
        // past the limit every other upload would be refused too: say so for them instead of trying each
        if (row.limited) {
          for (const q of queue.splice(0)) Object.assign(q.row, { cls: 'error', text: 'not sent', why: 'the upload limit was reached; try again in ' + waitText(resetIn()), done: true });
          onChange();
        }
      }
    } finally { running = false; }
  }

  async function sendOne(f, row, onChange) {
    if (!EXT.test(f.name)) { Object.assign(row, { cls: 'error', text: 'skipped', why: 'only raw .dm_57 / .dm_59 / .dm_60 demo files are accepted (not zips)', done: true }); onChange(); return; }
    // the last answer said no uploads are left this hour: do not send the file just to have it refused
    if (rate && rate.remaining === 0 && resetIn() > 0) { Object.assign(row, { cls: 'error', text: 'not sent', why: 'the upload limit was reached; try again in ' + waitText(resetIn()), done: true, limited: true }); onChange(); return; }
    Object.assign(row, { cls: 'sending', text: 'sending' }); onChange();
    try {
      const res = await post('/upload?name=' + encodeURIComponent(f.name), f, (n, total) => {
        row.sent = n / total;
        if (n >= total) Object.assign(row, { text: 'analysing', why: 'lining it up with the other demos' });
        onChange();
      });
      Object.assign(row, describe(res), { sent: 1, matchId: res.demo.match_id, done: true });
    } catch (e) { Object.assign(row, { cls: 'error', text: 'error', why: e.message, done: true, limited: e.status === 429 }); }
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
    // A file dropped anywhere else on the page would be opened by the browser and the page left: while the zone
    // is showing, the whole page takes the drop.
    const files = e => e.dataTransfer && [...(e.dataTransfer.types || [])].includes('Files');
    const live = () => zone.offsetParent !== null;
    document.addEventListener('dragover', e => { if (files(e) && live()) { e.preventDefault(); zone.classList.add('over'); } });
    document.addEventListener('dragleave', e => { if (!e.relatedTarget) zone.classList.remove('over'); });
    document.addEventListener('drop', e => {
      if (e.defaultPrevented || !files(e) || !live()) return;
      e.preventDefault(); zone.classList.remove('over'); handle([...e.dataTransfer.files]);
    });
  }

  // The site's header and footer, the same as on the main pages (there is no "Demos" in them: the tool is
  // reached from a blurb on the home page).  The pages carry a plain one too, for when scripts are off.
  function chrome() {
    const h = document.querySelector('header.s4-header'), f = document.querySelector('footer.s4-foot');
    if (h) h.innerHTML = '<a class="brand" href="/"><img src="/img/s4nd-logo.svg" alt="S4NDMoD26" width="135" height="48"><div><h1>S4NDMoD<span>26</span></h1><small>A Return to Castle Wolfenstein mod</small></div></a>' +
      '<nav class="s4-nav" aria-label="Site"><a href="/">Play</a><a class="live" href="/paths.html?live=1">\u25cf Live</a><a href="/stats.html">Stats</a><a href="/contact.html">Contact</a></nav>';
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
  return { title, esc, clean, clock, access, api, describe, uploadAll, renderSent, dropZone, when, mode, setRate, rateText };
})();
