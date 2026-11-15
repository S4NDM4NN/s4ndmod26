// The event feed's wording and grouping, shared by the stats page (game.html) and the 3D replay (paths.html), so both say the same thing
// about the same event.  The data is the status API's feed (kills, revives, objectives, joins, and the support events), one object per
// event: { t_ms, type, actor, actor_team, target, target_team, weapon, headshot, pos, actor_slot, target_slot }.
(function (w) {
  var esc = function (s) { return String(s == null ? '' : s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'); };

  // What the filter chips switch on and off.  `off` ones start hidden.
  var GROUPS = {
    kills:      { label: 'Kills',      types: ['kill', 'teamkill', 'suicide'] },
    revives:    { label: 'Revives',    types: ['revive'] },
    objectives: { label: 'Objectives', types: ['plant', 'defuse', 'capture', 'steal', 'return', 'denial'] },
    support:    { label: 'Support',    types: ['medpack', 'ammo', 'strike', 'tapout'], off: true },
    players:    { label: 'Players',    types: ['join', 'leave', 'end'], off: true }
  };

  function time(ms) {
    var s = Math.floor((ms || 0) / 1000);
    return Math.floor(s / 60) + ':' + String(s % 60).padStart(2, '0');
  }

  // A name with a dot for its team.  `nm` turns a plain name into coloured HTML (the page knows the raw coloured names).
  function name(n, team, nm) {
    if (!n) return '';
    return '<span class="dot ' + (team === 'axis' ? 'ax' : team === 'allies' ? 'al' : 'nt') + '"></span><span class="nm">' + (nm ? nm(n) : esc(n)) + '</span>';
  }

  // The sentence for an event: { cls, html }.  cls marks the kinds a page may style (tk, obj, meta, sup).
  function line(e, nm) {
    var a = name(e.actor, e.actor_team, nm), t = name(e.target, e.target_team, nm);
    var wp = e.weapon ? ' with <span class="w">' + esc(e.weapon) + '</span>' : '';
    var hs = e.headshot ? '<span class="hs">headshot</span>' : '';
    var cls = '', text = '';
    switch (e.type) {
      case 'kill':     text = a + ' killed ' + t + wp + hs; break;
      case 'teamkill': cls = 'tk'; text = a + ' killed teammate ' + t + wp; break;
      case 'suicide':  text = e.weapon && e.weapon !== 'Suicide' ? a + ' died <span class="w">(' + esc(e.weapon) + ')</span>' : a + ' killed themself'; break;
      case 'revive':   text = a + ' revived ' + t; break;
      case 'plant':    cls = 'obj'; text = a + ' planted the explosives'; break;
      case 'defuse':   cls = 'obj'; text = a + ' defused the explosives'; break;
      case 'capture':  cls = 'obj'; text = a + ' captured the objective'; break;
      case 'steal':    cls = 'obj'; text = a + ' picked up the objective'; break;
      case 'return':   cls = 'obj'; text = a + ' returned the objective'; break;
      case 'denial':   cls = 'obj'; text = a + ' denied the objective'; break;
      case 'medpack':  cls = 'sup'; text = a + ' gave ' + t + ' a medpack'; break;
      case 'ammo':     cls = 'sup'; text = a + ' gave ' + t + ' ammo'; break;
      case 'strike':   cls = 'sup'; text = a + ' called in ' + (e.weapon === 'Airstrike' ? 'an airstrike' : e.weapon === 'Artillery' ? 'artillery' : 'a strike'); break;
      case 'tapout':   cls = 'sup'; text = a + ' tapped out'; break;
      case 'join':     cls = 'meta'; text = a + ' joined'; break;
      case 'leave':    cls = 'meta'; text = a + ' left'; break;
      case 'end':      cls = 'meta'; text = 'The match ended'; break;
      default:         text = esc(e.type);
    }
    return { cls: cls, html: text };
  }

  w.S4Feed = { GROUPS: GROUPS, time: time, name: name, line: line };
})(window);
