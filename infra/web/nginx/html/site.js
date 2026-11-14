// shared by every site page: the build number in the footer
fetch('/version.txt').then(function (r) { return r.ok ? r.text() : ''; }).then(function (v) {
  var el = document.getElementById('build-version');
  if (el && v && v.trim()) el.textContent = 'build ' + v.trim();
}).catch(function () {});

// The account button in the header: "Sign in" when nobody is signed in, the user's name with a small menu when
// someone is.  The last known name is kept in localStorage only to draw the right thing at once; the server's
// answer (GET /api/account/me) is what counts.  Names come from the server, so they go in with textContent.
(function () {
  var header = document.querySelector('.s4-header');
  if (!header) return;
  var KEY = 's4nd_acct';
  var box = document.createElement('div');
  box.className = 'acct';
  var icons = header.querySelector('.icon-link-group');
  var nav = header.querySelector('.s4-nav');
  // wide: top right of the header; phone and narrow windows: the last tab of the nav row, so it does not crowd the logo and title
  var phone = window.matchMedia ? window.matchMedia('(max-width: 720px)') : null;
  function place() {
    if (phone && phone.matches && nav) nav.append(box); else header.insertBefore(box, icons || null);
  }
  place();
  if (phone && phone.addEventListener) phone.addEventListener('change', place);

  function hint() { try { return localStorage.getItem(KEY) || ''; } catch (e) { return ''; } }
  function setHint(v) { try { if (v) localStorage.setItem(KEY, v); else localStorage.removeItem(KEY); } catch (e) {} }
  function el(tag, cls, text) { var e = document.createElement(tag); if (cls) e.className = cls; if (text !== undefined) e.textContent = text; return e; }

  function signedOut() {
    box.textContent = '';
    var a = el('a', 'btn acct-in', 'Sign in');
    a.href = '/account/';
    box.append(a);
  }

  function signedIn(me) {
    box.textContent = '';
    var d = el('details', 'acct-menu');
    var s = el('summary', 'btn acct-user');
    s.append(el('span', 'acct-name', me.username));
    s.setAttribute('aria-label', 'Signed in as ' + me.username + '. Account menu');
    var m = el('div', 'acct-pop');
    m.append(el('div', 'acct-who', 'Signed in as ' + me.username));
    if (me.email_verified === false) {
      var warn = el('a', 'acct-warn', 'Confirm your email address');
      warn.href = '/account/';
      m.append(warn);
    }
    var mine = el('a', '', 'My account');
    mine.href = '/account/';
    m.append(mine);
    if ((me.permissions || []).indexOf('admin') >= 0) {
      var adm = el('a', '', 'Admin');
      adm.href = '/admin/';
      m.append(adm);
    }
    var out = el('button', 'acct-out', 'Sign out');
    out.type = 'button';
    out.addEventListener('click', function () {
      fetch('/api/account/logout', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })
        .catch(function () {}).then(function () { setHint(''); signedOut(); if (/^\/(account|admin)\//.test(location.pathname)) location.reload(); });
    });
    m.append(out);
    d.append(s, m);
    box.append(d);
    document.addEventListener('click', function (ev) { if (!d.contains(ev.target)) d.removeAttribute('open'); });
    document.addEventListener('keydown', function (ev) { if (ev.key === 'Escape') d.removeAttribute('open'); });
  }

  var h = hint();
  if (h) signedIn({ username: h, permissions: [], email_verified: true }); else signedOut();
  fetch('/api/account/me', { headers: { Accept: 'application/json' }, cache: 'no-store' }).then(function (r) {
    if (r.status === 401) { setHint(''); signedOut(); return null; }
    return r.ok ? r.json() : null;
  }).then(function (me) {
    if (me && me.username) { setHint(me.username); signedIn(me); }
  }).catch(function () {});
})();
