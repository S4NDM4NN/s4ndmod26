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
    who.append(el('strong', '', v.name || v.hostname || v.game_addr));
    if (v.official) who.append(' ', el('span', 'tag-official', 'Official'));
    who.append(el('div', 'srv-addr', v.game_addr));
    const full = v.max_players && v.players >= v.max_players;
    const meta = el('div', 'srv-meta');
    meta.append(el('span', '', v.map || '—'), el('span', full ? 'full' : '', (v.players || 0) + ' / ' + (v.max_players || '?') + ' players'));
    const join = el('a', 'btn-play small', 'Join');
    join.href = '/play/?server=' + encodeURIComponent(v.server_id);
    join.setAttribute('aria-label', 'Join ' + (v.name || v.game_addr));
    r.append(who, meta, join);
    return r;
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

  return { render, load };
})();
