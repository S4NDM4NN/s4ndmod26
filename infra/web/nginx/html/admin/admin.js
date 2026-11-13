// Shared by the admin area: JSON calls, small DOM helpers and a pager.  A section is a function registered with
// A.section(id, title, render); render(root) fills its panel and may use A.pager for any long list.
'use strict';
const A = (() => {
  const $ = id => document.getElementById(id);
  const sections = [];

  async function api(method, path, body) {
    const opt = { method, credentials: 'same-origin', headers: {} };
    if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
    const r = await fetch(path, opt);
    let data = null;
    if (r.status !== 204) { try { data = await r.json(); } catch (e) { /* no body */ } }
    if (!r.ok) { const err = new Error((data && data.error) || ('error ' + r.status)); err.status = r.status; throw err; }
    return data;
  }

  function el(tag, attrs, ...kids) {
    const e = document.createElement(tag);
    for (const k in attrs || {}) {
      if (k === 'class') e.className = attrs[k];
      else if (k === 'on') for (const ev in attrs.on) e.addEventListener(ev, attrs.on[ev]);
      else e.setAttribute(k, attrs[k]);
    }
    for (const kid of kids) e.append(kid);
    return e;
  }

  // A pager for a long list.  load(page, per) must return {total, ...}; render(result) draws the rows.  Returns
  // {el, reload(page?), reset()}: put el where the controls go; reset() goes back to page 1 (a new search).
  function pager({ per = 25, load, render }) {
    let page = 1, total = 0;
    const info = el('span', { class: 'quiet' });
    const prev = el('button', { class: 'act', type: 'button' }, '← Previous');
    const next = el('button', { class: 'act', type: 'button' }, 'Next →');
    const root = el('div', { class: 'pager' }, prev, info, next);
    async function go(p) {
      const res = await load(p, per);
      total = res.total;
      const pages = Math.max(1, Math.ceil(total / per));
      if (p > pages) return go(pages);        // the list got shorter: land on the last page
      page = p;
      render(res);
      prev.disabled = page <= 1; next.disabled = page >= pages;
      info.textContent = total ? ((page - 1) * per + 1) + '–' + Math.min(page * per, total) + ' of ' + total + ' · page ' + page + ' of ' + pages : 'nothing here';
      prev.hidden = next.hidden = pages <= 1;
    }
    prev.addEventListener('click', () => go(page - 1));
    next.addEventListener('click', () => go(page + 1));
    return { el: root, reload: p => go(p || page), reset: () => go(1) };
  }

  function section(id, title, render) { sections.push({ id, title, render }); }

  // Draws the navigation and shows the section named in the address (#accounts), or the first one.
  function start() {
    const nav = $('nav'), main = $('main');
    const drawn = new Set();
    function show() {
      const id = location.hash.slice(1) || sections[0].id;
      const cur = sections.find(s => s.id === id) || sections[0];
      nav.querySelectorAll('a').forEach(a => a.classList.toggle('on', a.dataset.id === cur.id));
      sections.forEach(s => { const p = $('sec-' + s.id); if (p) p.hidden = s.id !== cur.id; });
      if (!drawn.has(cur.id)) { drawn.add(cur.id); cur.render($('sec-' + cur.id)); }
    }
    sections.forEach(s => {
      nav.append(el('a', { href: '#' + s.id, 'data-id': s.id }, s.title));
      main.append(el('section', { id: 'sec-' + s.id, class: 'panel box', hidden: '' }));
    });
    window.addEventListener('hashchange', show);
    show();
  }

  function say(node, text, kind) { node.textContent = text || ''; node.className = 'msg' + (kind ? ' ' + kind : ''); }

  return { $, api, el, pager, section, start, say };
})();
