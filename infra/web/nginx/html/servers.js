// The community server list: used by the front page (first few) and /servers.html (all, paged).
// Server names and addresses come from hosts, so everything is put in with textContent, never as HTML.
'use strict';
const S4Servers = (() => {
  function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }

  // One row per server: name, map, players, address and a Join button.
  function row(v) {
    const r = el('div', 'srvrow');
    const who = el('div', 'srv-who');
    // the server's own name from its last status poll (set in its server.cfg), not the label given when it registered
    const title = v.hostname || v.name || v.game_addr;
    who.append(el('strong', '', title));
    if (v.official) who.append(' ', el('span', 'tag-official', 'Official'));
    who.append(el('div', 'srv-addr', v.game_addr));
    const full = v.max_players && v.players >= v.max_players;
    const meta = el('div', 'srv-meta');
    meta.append(el('span', '', v.map || '—'), el('span', full ? 'full' : '', (v.players || 0) + ' / ' + (v.max_players || '?') + ' players'));
    const join = el('a', 'btn-play small', 'Join');
    join.href = '/play/?server=' + encodeURIComponent(v.server_id);
    join.setAttribute('aria-label', 'Join ' + title);
    r.append(who, meta, join);
    return r;
  }

  // One tile per official server: a compact card for a grid, with how full it is.
  function tile(v) {
    const t = el('div', 'srvtile');
    const title = v.hostname || v.name || v.game_addr;
    const head = el('div', 'tile-head');
    head.append(el('span', 'tag-official', 'Official'));
    const max = v.max_players || 0, n = v.players || 0;
    const full = max && n >= max;
    const meta = el('div', 'tile-meta');
    meta.append(el('span', 'tile-map', v.map || '\u2014'), el('span', full ? 'full' : 'tile-count', n + ' / ' + (max || '?')));
    const bar = el('div', 'tile-bar' + (full ? ' full' : ''));
    const fill = el('i');
    fill.style.width = (max ? Math.min(100, Math.round(100 * n / max)) : 0) + '%';
    bar.append(fill);
    const join = el('a', 'btn-play small', 'Join');
    join.href = '/play/?server=' + encodeURIComponent(v.server_id);
    join.setAttribute('aria-label', 'Join ' + title);
    t.append(head, el('div', 'tile-name', title), meta, bar, join);
    return t;
  }

  // Fills box with a grid of official tiles; limit shows the first few and a button for the rest.
  function renderTiles(box, servers, limit) {
    box.textContent = '';
    const grid = el('div', 'srv-grid');
    servers.forEach((v, i) => { const t = tile(v); if (limit && i >= limit) t.hidden = true; grid.append(t); });
    box.append(grid);
    if (limit && servers.length > limit) {
      const more = el('button', 'btn srv-more', 'Show all ' + servers.length + ' official servers');
      more.type = 'button';
      more.addEventListener('click', () => { grid.querySelectorAll('[hidden]').forEach(t => { t.hidden = false; }); more.remove(); });
      box.append(more);
    }
  }

  // Fills box with the servers; the empty message is the host's invitation, never an empty table.
  function render(box, servers, empty) {
    box.textContent = '';
    if (!servers.length) {
      box.append(el('p', 'dim srv-empty', empty));
      return;
    }
    servers.forEach(v => box.append(row(v)));
  }

  async function load(kind, page, per) {
    const r = await fetch('/api/servers?kind=' + kind + '&page=' + page + '&per=' + per, { headers: { Accept: 'application/json' } });
    if (!r.ok) throw new Error('HTTP ' + r.status);
    return r.json();
  }

  return { render, renderTiles, load };
})();
