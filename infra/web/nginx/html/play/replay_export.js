/*
 * MP4 export of a play-of-the-game replay.
 *
 * In export mode (/play/?replay=NAME&export=1) the engine's video recorder (cl_avi.c) runs for
 * the playback part of the clip with a fixed timestep: every engine tick is exactly one video
 * frame, however long it took to draw.  Instead of writing an AVI it hands each RGBA frame and
 * each chunk of 16-bit stereo PCM to Module.s4ndExport, defined here.  Frames go through
 * WebCodecs (H.264, or VP9 where H.264 can't be encoded) and audio through AAC (or Opus), and
 * mp4-muxer assembles the file.  A short title card is rendered in front of the clip and the
 * clip fades out into the logo.  The page itself only shows a progress screen; the game keeps
 * rendering underneath it, which the export needs.
 *
 * The engine skips a tick while busy() is true, so a slow encoder just slows the render down.
 */
(function () {
  'use strict';

  var CARD_SECONDS = 3;
  var OUTRO_FADE_SECONDS = 1;
  var OUTRO_HOLD_SECONDS = 2.5;
  var VIDEO_BITRATE = 24000000;
  var VP9_BITRATE = 40000000;  // VP9 (Firefox) gets more headroom: its encoder ignores constant-quality mode and starves on busy frames
  var KEYFRAME_EVERY = 60;     // a quality dip can last at most to the next keyframe (1 s)
  var MAX_VIDEO_QUEUE = 6;
  var MAX_AUDIO_QUEUE = 40;
  var BRAND = 'S4NDMoD26';
  var URL_TEXT = 's4ndmod.com';
  var LOGO_PK3 = '/downloads/main/demopak0.pk3';           // the game data the player already downloads (the music)
  var MUSIC_FILE = 'sound/music/x_action.wav';             // the main menu music
  var MUSIC_FULL = 0.7;                                    // level under the cards
  var MUSIC_DUCKED = 0.22;                                 // level under the game's own sound

  var info = {};          // title card text and expected clip length, set by init()
  var ui = null;
  var cfg = null;         // codec choice, found by probe()
  var ex = null;          // the running export
  var logo = null;        // canvas holding the Wolfenstein logo, or null if it couldn't be loaded
  var music = null;       // {rate, left, right} decoded menu music, or null
  var shot = null;        // the map's loading screen (levelshot) as an ImageBitmap, or null

  function us(frameIndex, fps) { return Math.round(frameIndex * 1000000 / fps); }

  // Every frame is given the same colour space, explicitly: sRGB picture, BT.709 matrix, limited (video) range.
  // Left to itself the encoder picks its own - Chrome's came out full range while a frame taken from a canvas
  // is converted as limited range - and a player that reads the wrong range crushes the dark parts of the
  // picture to black (a night scene on a dark map is mostly dark parts).
  var FRAME_COLORSPACE = { primaries: 'bt709', transfer: 'iec61966-2-1', matrix: 'bt709', fullRange: false };

  // Options for encoding one frame: keyframe flag, plus the quantizer when the encoder runs in constant-quality mode.
  function encodeOpts(keyFrame) {
    var o = { keyFrame: keyFrame };
    if (cfg && cfg.video && cfg.video.quantizer != null) o.vp9 = { quantizer: cfg.video.quantizer };
    return o;
  }

  function rgbaFrame(data, width, height, timestamp, duration) {
    return new VideoFrame(data, { format: 'RGBA', codedWidth: width, codedHeight: height, timestamp: timestamp,
                                  duration: duration, colorSpace: FRAME_COLORSPACE });
  }

  // A frame from a canvas, as a buffer frame so it can carry the colour space above.
  function canvasFrame(canvas, timestamp, duration) {
    var ctx = canvas.getContext('2d');
    return rgbaFrame(ctx.getImageData(0, 0, canvas.width, canvas.height).data, canvas.width, canvas.height, timestamp, duration);
  }

  // Some browsers (Firefox) hand back encoded chunks without a duration, which the muxer refuses.  Rebuild
  // such a chunk with the duration it should have.
  function withDuration(chunk, durationUs, Ctor) {
    if (chunk.duration != null) return chunk;
    var data = new Uint8Array(chunk.byteLength);
    chunk.copyTo(data);
    return new Ctor({ type: chunk.type, timestamp: chunk.timestamp, duration: durationUs, data: data });
  }
  function sleep(ms) { return new Promise(function (r) { setTimeout(r, ms); }); }

  // ---- logo and music ---------------------------------------------------------------------
  // The music is part of the game data (a WAV inside demopak0.pk3), which the player already has
  // from the server, so it is read from there with a few range requests instead of being
  // redistributed with the site.  If anything about that fails the export just goes without it.

  async function range(url, header) {
    var r = await fetch(url, { headers: { Range: header } });
    if (r.status !== 206 && r.status !== 200) throw new Error('range request failed: ' + r.status);
    var total = 0;
    var m = /\/(\d+)$/.exec(r.headers.get('Content-Range') || '');
    if (m) total = parseInt(m[1], 10);
    return { data: new Uint8Array(await r.arrayBuffer()), total: total };
  }

  // Reads one file out of the pk3 (a zip) with a few range requests.
  var pk3Directory = null;
  async function readPk3Entry(wanted) {
    if (!pk3Directory) {
      // end of central directory, in the last bytes of the zip
      var size = (await range(LOGO_PK3, 'bytes=0-0')).total;     // an explicit range works on every server
      if (!size) throw new Error('unknown pk3 size');
      var tail = await range(LOGO_PK3, 'bytes=' + Math.max(0, size - 70000) + '-' + (size - 1));
      var t = tail.data, dv = new DataView(t.buffer, t.byteOffset, t.byteLength);
      var e = -1;
      for (var i = t.length - 22; i >= 0; i--) { if (dv.getUint32(i, true) === 0x06054b50) { e = i; break; } }
      if (e < 0) throw new Error('no zip directory');
      var cdSize = dv.getUint32(e + 12, true), cdOff = dv.getUint32(e + 16, true);
      pk3Directory = (await range(LOGO_PK3, 'bytes=' + cdOff + '-' + (cdOff + cdSize - 1))).data;
    }
    var cd = pk3Directory;
    var cdv = new DataView(cd.buffer, cd.byteOffset, cd.byteLength);
    var p = 0, entry = null;
    while (p + 46 <= cd.length && cdv.getUint32(p, true) === 0x02014b50) {
      var nameLen = cdv.getUint16(p + 28, true), extraLen = cdv.getUint16(p + 30, true), commentLen = cdv.getUint16(p + 32, true);
      var name = new TextDecoder().decode(cd.subarray(p + 46, p + 46 + nameLen)).toLowerCase();
      if (name === wanted) {
        entry = { method: cdv.getUint16(p + 10, true), csize: cdv.getUint32(p + 20, true), off: cdv.getUint32(p + 42, true) };
        break;
      }
      p += 46 + nameLen + extraLen + commentLen;
    }
    if (!entry) throw new Error(wanted + ' not found');
    var lh = (await range(LOGO_PK3, 'bytes=' + entry.off + '-' + (entry.off + 29))).data;
    var ldv = new DataView(lh.buffer, lh.byteOffset, lh.byteLength);
    var start = entry.off + 30 + ldv.getUint16(26, true) + ldv.getUint16(28, true);
    var comp = (await range(LOGO_PK3, 'bytes=' + start + '-' + (start + entry.csize - 1))).data;
    if (entry.method === 0) return comp;
    if (entry.method !== 8) throw new Error('unsupported zip method');
    var ds = new DecompressionStream('deflate-raw');
    return new Uint8Array(await new Response(new Blob([comp]).stream().pipeThrough(ds)).arrayBuffer());
  }

  // The red wolf emblem (iortcw/misc/wolf512.png).  Its grey outline is recoloured white so it reads
  // red and white against the dark cards.
  async function loadLogo() {
    var resp = await fetch('wolf_logo.png');
    if (!resp.ok) throw new Error('wolf_logo.png: ' + resp.status);
    var bmp = await createImageBitmap(await resp.blob());
    var cv = document.createElement('canvas');
    cv.width = bmp.width; cv.height = bmp.height;
    var ctx = cv.getContext('2d');
    ctx.drawImage(bmp, 0, 0);
    var img = ctx.getImageData(0, 0, cv.width, cv.height), d = img.data;
    for (var i = 0; i < d.length; i += 4) {
      var r = d[i], g = d[i + 1], b = d[i + 2];
      if (Math.abs(r - g) < 24 && Math.abs(g - b) < 24) {          // the grey outline (and its anti-aliasing)
        var k = Math.min(255, Math.round(g * 2));                  // 128 -> 255
        d[i] = d[i + 1] = d[i + 2] = k;
      } else {                                                      // the dark red fill, a little brighter
        d[i] = Math.min(255, Math.round(r * 1.55)); d[i + 1] = Math.round(g * 1.2); d[i + 2] = Math.round(b * 1.2);
      }
    }
    ctx.putImageData(img, 0, 0);
    return cv;
  }

  // The map's loading screen.  The status API looks for levelshots/<map> in every pk3 the server has
  // (so custom maps work); if that isn't available, the demo pak is read directly.  The generic
  // "unknown map" picture is only used when nobody has a levelshot for the map.
  async function loadLevelshot(map) {
    var name = String(map).toLowerCase();
    async function fromApi(n) {
      var r = await fetch('/api/levelshot/' + encodeURIComponent(n));
      if (!r.ok) throw new Error('no levelshot for ' + n);
      return createImageBitmap(await r.blob());
    }
    async function fromPk3(n) {
      var data = await readPk3Entry('levelshots/' + n + '.jpg');
      return createImageBitmap(new Blob([data], { type: 'image/jpeg' }));
    }
    var attempts = [
      function () { return fromApi(name); }, function () { return fromPk3(name); },
      function () { return fromApi('unknownmap'); }, function () { return fromPk3('unknownmap'); }
    ];
    for (var i = 0; i < attempts.length; i++) {
      try { return await attempts[i](); } catch (e) { /* try the next source */ }
    }
    throw new Error('no loading screen');
  }

  async function loadMusic() {
    var wav = await readPk3Entry(MUSIC_FILE);
    var rate = 48000;
    var buf = await new OfflineAudioContext(2, 1, rate).decodeAudioData(wav.buffer.slice(wav.byteOffset, wav.byteOffset + wav.byteLength));
    return { rate: buf.sampleRate, length: buf.length,
             left: buf.getChannelData(0), right: buf.numberOfChannels > 1 ? buf.getChannelData(1) : buf.getChannelData(0) };
  }

  // ---- codec selection ------------------------------------------------------------------

  var H264 = ['avc1.64002A', 'avc1.640028', 'avc1.4D402A', 'avc1.42002A'];   // High/Main/Baseline, level 4.2
  var VP9 = ['vp09.00.41.08', 'vp09.00.40.08'];

  // isConfigSupported only says an encoder exists.  Encode a few real frames and check that what comes back is
  // something the MP4 muxer can use: the first chunk at time 0, and (for H.264/AAC) the codec configuration.
  // Some browsers' encoders pass isConfigSupported and then produce a track nothing can play.
  function testVideoEncode(conf, width, height, fps, quantizer) {
    return new Promise(function (resolve) {
      var first = null, count = 0, done = false, enc = null;
      function finish(reason) {
        if (done) return;
        done = true;
        clearTimeout(timer);
        try { if (enc) enc.close(); } catch (e) { /* already closed */ }
        resolve(reason);                       // null = usable, otherwise why not
      }
      var timer = setTimeout(function () { finish('the encoder produced nothing within 10 s'); }, 10000);
      try {
        enc = new VideoEncoder({
          output: function (chunk, meta) { count++; if (!first) first = { ts: chunk.timestamp, meta: meta }; },
          error: function (e) { finish('encoder error: ' + (e && e.message)); }
        });
        enc.configure(conf);
        var cv = document.createElement('canvas');
        cv.width = width; cv.height = height;
        var ctx = cv.getContext('2d');
        for (var i = 0; i < 8; i++) {
          ctx.fillStyle = 'rgb(' + (i * 30) + ',60,90)';
          ctx.fillRect(0, 0, width, height);
          var f = canvasFrame(cv, us(i, fps), us(1, fps));
          enc.encode(f, quantizer != null ? { keyFrame: i === 0, vp9: { quantizer: quantizer } } : { keyFrame: i === 0 });
          f.close();
        }
        enc.flush().then(function () {
          if (!count) return finish('no frames came out');
          // the muxer shifts a non-zero first timestamp to 0 (firstTimestampBehavior 'offset'), so that is fine
          if (conf.avc && !(first.meta && first.meta.decoderConfig && first.meta.decoderConfig.description)) {
            return finish('no codec configuration (avcC) came with the first frame');
          }
          finish(null);
        }, function (e) { finish('encoder error: ' + (e && e.message)); });
      } catch (e) { finish('could not start: ' + (e && e.message)); }
    });
  }

  function testAudioEncode(conf) {
    return new Promise(function (resolve) {
      var first = null, count = 0, done = false, enc = null;
      function finish(ok) {
        if (done) return;
        done = true;
        clearTimeout(timer);
        try { if (enc) enc.close(); } catch (e) { /* already closed */ }
        resolve(ok);
      }
      var timer = setTimeout(function () { finish(false); }, 6000);
      try {
        enc = new AudioEncoder({
          output: function (chunk, meta) { count++; if (!first) first = { ts: chunk.timestamp, meta: meta }; },
          error: function () { finish(false); }
        });
        enc.configure(conf);
        var n = Math.round(conf.sampleRate / 5);
        enc.encode(new AudioData({ format: 's16', sampleRate: conf.sampleRate, numberOfFrames: n, numberOfChannels: 2,
                                    timestamp: 0, data: new Int16Array(n * 2) }));
        enc.flush().then(function () {
          var ok = count > 0 && first && first.ts === 0;
          if (ok && conf.codec.indexOf('mp4a') === 0) ok = !!(first.meta && first.meta.decoderConfig && first.meta.decoderConfig.description);
          finish(!!ok);
        }, function () { finish(false); });
      } catch (e) { finish(false); }
    });
  }

  async function pickVideo(width, height, fps, notes) {
    var tries = H264.map(function (c) { return { muxer: 'avc', codec: c, extra: { avc: { format: 'avc' } } }; })
      .concat(VP9.map(function (c) { return { muxer: 'vp9', codec: c, extra: {} }; }));
    for (var i = 0; i < tries.length; i++) {
      var t = tries[i];
      var isVp9 = t.muxer === 'vp9';
      var conf = Object.assign({ codec: t.codec, width: width, height: height, framerate: fps, latencyMode: 'quality',
                                 bitrate: isVp9 ? VP9_BITRATE : VIDEO_BITRATE }, isVp9 ? { bitrateMode: 'variable' } : {}, t.extra);
      try {
        var r = await VideoEncoder.isConfigSupported(conf);
        if (!r || !r.supported) { notes.push(t.codec + ': not supported at ' + width + 'x' + height); continue; }
        var reason = await testVideoEncode(conf, width, height, fps, null);
        if (!reason) return { muxer: t.muxer, config: conf, quantizer: null };
        notes.push(t.codec + ': ' + reason);
      } catch (e) { notes.push(t.codec + ': ' + (e && e.message)); }
    }
    return null;
  }

  async function pickAudio(rate) {
    var tries = [{ muxer: 'aac', codec: 'mp4a.40.2' }, { muxer: 'opus', codec: 'opus' }];
    for (var i = 0; i < tries.length; i++) {
      var conf = { codec: tries[i].codec, sampleRate: rate, numberOfChannels: 2, bitrate: 192000 };
      try {
        var r = await AudioEncoder.isConfigSupported(conf);
        if (r && r.supported && await testAudioEncode(conf)) return { muxer: tries[i].muxer, config: conf };
      } catch (e) { /* try the next one */ }
    }
    return null;
  }

  // Everything that has to be known before the engine's synchronous open() call.
  async function probe(width, height, fps) {
    if (typeof VideoEncoder === 'undefined' || typeof AudioEncoder === 'undefined' || typeof VideoFrame === 'undefined') {
      throw new Error('This browser has no WebCodecs support. Use a current Chrome, Edge or Safari.');
    }
    if (typeof Mp4Muxer === 'undefined') throw new Error('The MP4 muxer failed to load.');
    var notes = [];
    var video = await pickVideo(width, height, fps, notes);
    if (notes.length) console.warn('[export] encoders skipped: ' + notes.join('; '));
    if (!video) throw new Error('This browser cannot encode ' + width + '×' + height + ' video.');
    var audio = {};
    for (var rate of [48000, 44100, 22050]) audio[rate] = await pickAudio(rate);
    return { video: video, audio: audio, width: width, height: height, fps: fps, notes: notes };
  }

  // ---- title card and outro ---------------------------------------------------------------

  function font(px, weight) { return (weight || '700') + ' ' + px + 'px "Segoe UI", "Helvetica Neue", Arial, sans-serif'; }

  function drawLogo(ctx, cx, top, width) {
    if (!logo) return 0;
    var h = width * logo.height / logo.width;
    ctx.save();
    ctx.imageSmoothingEnabled = true;
    ctx.imageSmoothingQuality = 'high';
    ctx.drawImage(logo, cx - width / 2, top, width, h);
    ctx.restore();
    return h;
  }

  function background(ctx, w, h) {
    var g = ctx.createRadialGradient(w / 2, h / 2, h * 0.1, w / 2, h / 2, h * 0.9);
    g.addColorStop(0, '#2a2f38');
    g.addColorStop(1, '#07090c');
    ctx.globalAlpha = 1;
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, w, h);
  }

  // The map's loading screen, sitting angled at the right behind the text, drifting in slowly.
  function drawLevelshot(ctx, w, h, t, total) {
    if (!shot) return;
    var ease = 1 - Math.pow(1 - Math.min(1, t / 1.4), 3);            // slides in over the first 1.4 s
    var pw = w * 0.3, ph = pw * shot.height / shot.width;
    var cx = w * 0.76 + (1 - ease) * w * 0.05, cy = h * 0.5;
    var scale = 1 + 0.05 * (t / total);                               // a slow push-in
    ctx.save();
    ctx.translate(cx, cy);
    ctx.rotate(-0.1);                                                 // about -6 degrees
    ctx.transform(1, -0.07, 0, 1, 0, 0);                              // a little perspective-like skew
    ctx.scale(scale, scale);
    ctx.shadowColor = 'rgba(0,0,0,0.75)';
    ctx.shadowBlur = h * 0.05;
    ctx.shadowOffsetY = h * 0.02;
    ctx.fillStyle = '#e8e2d0';
    var b = h * 0.008;                                                // a thin light border, like a print
    ctx.fillRect(-pw / 2 - b, -ph / 2 - b, pw + b * 2, ph + b * 2);
    ctx.shadowColor = 'transparent';
    ctx.globalAlpha = 0.92 * ctx.globalAlpha;
    ctx.drawImage(shot, -pw / 2, -ph / 2, pw, ph);
    // darken the edge nearest the text so it reads as background
    var g = ctx.createLinearGradient(-pw / 2, 0, pw / 2, 0);
    g.addColorStop(0, 'rgba(7,9,12,0.55)');
    g.addColorStop(0.45, 'rgba(7,9,12,0)');
    ctx.fillStyle = g;
    ctx.fillRect(-pw / 2, -ph / 2, pw, ph);
    ctx.restore();
  }

  function drawCard(ctx, w, h, t, total) {
    var fade = Math.max(0, Math.min(1, t / 0.5, (total - t) / 0.5));
    background(ctx, w, h);
    ctx.globalAlpha = fade;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';

    drawLevelshot(ctx, w, h, t, total);
    ctx.globalAlpha = fade;
    var tx = shot ? w * 0.36 : w / 2;                                 // text moves left to make room
    var tw = shot ? w * 0.68 : w * 0.86;

    drawLogo(ctx, tx, h * 0.05, h * 0.3);

    ctx.fillStyle = '#ffffff';
    ctx.font = font(h * 0.06, '800');
    ctx.fillText(BRAND, tx, h * 0.43);

    ctx.fillStyle = '#c8a24a';
    ctx.font = font(h * 0.036, '600');
    ctx.fillText(info.kind === 'highlight' ? 'H I G H L I G H T' : 'P L A Y   O F   T H E   G A M E', tx, h * 0.54);

    ctx.fillStyle = '#ffffff';
    var name = info.player || 'Unknown';
    var size = h * 0.12;
    ctx.font = font(size, '800');
    while (ctx.measureText(name).width > tw && size > 20) { size -= 4; ctx.font = font(size, '800'); }
    ctx.fillText(name, tx, h * 0.66);

    ctx.fillStyle = 'rgba(200,162,74,0.9)';
    ctx.fillRect(tx - w * 0.1, h * 0.745, w * 0.2, 3);

    ctx.fillStyle = '#9aa4b2';
    ctx.font = font(h * 0.036, '500');
    var line = [info.map, info.when].filter(Boolean).join('   ·   ');
    if (line) ctx.fillText(line, tx, h * 0.805);

    ctx.fillStyle = '#c8a24a';
    ctx.font = font(h * 0.034, '600');
    ctx.fillText(URL_TEXT, tx, h * 0.93);
    ctx.globalAlpha = 1;
  }

  // The closing screen: the logo on black, large.
  function drawEndScreen(ctx, w, h) {
    ctx.globalAlpha = 1;
    ctx.fillStyle = '#000';
    ctx.fillRect(0, 0, w, h);
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    var lw = h * 0.56, lhEst = logo ? lw * logo.height / logo.width : 0;
    var top = (h - lhEst) / 2 - h * 0.08;
    var lh = drawLogo(ctx, w / 2, top, lw) || 0;
    var y = logo ? top + lh + h * 0.0 : h * 0.45;
    ctx.fillStyle = '#ffffff';
    ctx.font = font(h * 0.06, '800');
    ctx.fillText(BRAND, w / 2, y + h * 0.05);
    ctx.fillStyle = '#c8a24a';
    ctx.font = font(h * 0.034, '600');
    ctx.fillText(URL_TEXT, w / 2, y + h * 0.12);
  }

  // ---- progress screen ------------------------------------------------------------------

  function makeUi() {
    if (ui) return ui;
    var el = document.createElement('div');
    el.id = 'exportOverlay';
    // opaque and full-page: the clip is not shown while it renders (it keeps drawing underneath)
    el.style.cssText = 'position:fixed;inset:0;z-index:50;background:radial-gradient(ellipse at center,#2a2f38 0,#07090c 75%);' +
      'color:#e6e9ee;font:14px/1.4 system-ui,sans-serif;display:flex;flex-direction:column;align-items:center;' +
      'justify-content:center;gap:10px;padding:16px;text-align:center';
    el.innerHTML =
      '<div id="exportLogo" style="width:min(36vw,170px)"></div>' +
      '<div style="font:800 clamp(20px,5vw,32px) system-ui,sans-serif;letter-spacing:.04em">' + BRAND + '</div>' +
      '<div id="exportText" style="font-weight:600;font-size:16px;margin-top:6px">Preparing export…</div>' +
      '<div style="width:min(80vw,520px);height:10px;border-radius:5px;background:#252b35;overflow:hidden">' +
      '<div id="exportBar" style="height:100%;width:0;background:#c8a24a;transition:width .25s"></div></div>' +
      '<div id="exportSub" style="color:#9aa4b2;font-size:12px;min-height:1.4em"></div>' +
      '<div id="exportActions" style="display:none;gap:10px;margin-top:6px"></div>';
    document.body.appendChild(el);
    ui = { el: el, logo: el.querySelector('#exportLogo'), text: el.querySelector('#exportText'),
           bar: el.querySelector('#exportBar'), sub: el.querySelector('#exportSub'),
           actions: el.querySelector('#exportActions') };
    return ui;
  }

  function showLogoInUi() {
    if (!logo || !ui || ui.logo.firstChild) return;
    var c = document.createElement('canvas');
    c.width = logo.width; c.height = logo.height;
    c.style.cssText = 'width:100%;height:auto;display:block';
    c.getContext('2d').drawImage(logo, 0, 0);
    ui.logo.appendChild(c);
  }

  function setStatus(text, fraction, sub) {
    var u = makeUi();
    u.text.textContent = text;
    if (fraction != null) u.bar.style.width = Math.max(0, Math.min(100, fraction * 100)) + '%';
    if (sub != null) u.sub.textContent = sub;
  }

  function showError(err) {
    var msg = (err && err.message) || String(err);
    console.error('[export]', err);
    setStatus('Export failed', null, msg);
    makeUi().text.style.color = '#ff7b72';
  }

  function showDone(blob, filename) {
    var u = makeUi();
    var url = URL.createObjectURL(blob);
    setStatus('Done — ' + (blob.size / 1048576).toFixed(1) + ' MB', 1, filename);
    u.actions.style.display = 'flex';
    u.actions.innerHTML = '';
    var a = document.createElement('a');
    a.href = url;
    a.download = filename;
    a.textContent = 'Save MP4';
    a.style.cssText = 'background:#c8a24a;color:#111;padding:10px 22px;border-radius:6px;font-weight:700;text-decoration:none';
    u.actions.appendChild(a);
    // try to start the download straight away; the button covers browsers that block it
    setTimeout(function () { try { a.click(); } catch (e) { /* the button is still there */ } }, 300);
  }

  // ---- the engine-facing recorder ------------------------------------------------------

  function open(width, height, fps, audioRate, hasAudio) {
    if (!cfg) { showError(new Error('export was not prepared')); return false; }
    if (ex) return false;
    if (width !== cfg.width || height !== cfg.height || fps !== cfg.fps) {
      showError(new Error('The engine is rendering ' + width + '×' + height + '@' + fps +
        ', expected ' + cfg.width + '×' + cfg.height + '@' + cfg.fps + '.'));
      return false;
    }
    var audioCfg = hasAudio ? cfg.audio[audioRate] : null;

    var target = new Mp4Muxer.ArrayBufferTarget();
    var muxerOpts = {
      target: target,
      video: { codec: cfg.video.muxer, width: width, height: height, frameRate: fps },
      fastStart: 'in-memory',
      firstTimestampBehavior: 'offset'
    };
    if (audioCfg) muxerOpts.audio = { codec: audioCfg.muxer, numberOfChannels: 2, sampleRate: audioRate };
    var muxer = new Mp4Muxer.Muxer(muxerOpts);

    var failed = false;
    function fail(e) { if (!failed) { failed = true; showError(e); } }

    var venc = new VideoEncoder({
      output: function (chunk, meta) {
        try {
          muxer.addVideoChunk(withDuration(chunk, us(1, fps), EncodedVideoChunk), meta);
          ex.videoChunks++;
        } catch (e) { fail(e); }
      },
      error: fail
    });
    venc.configure(cfg.video.config);

    var aenc = null;
    if (audioCfg) {
      aenc = new AudioEncoder({
        output: function (chunk, meta) {
          // 20 ms for Opus, one 1024-sample frame for AAC
          var dur = audioCfg.muxer === 'opus' ? 20000 : Math.round(1024 * 1000000 / audioRate);
          try { muxer.addAudioChunk(withDuration(chunk, dur, EncodedAudioChunk), meta); } catch (e) { fail(e); }
        },
        error: fail
      });
      aenc.configure(audioCfg.config);
    }

    var cardFrames = Math.round(CARD_SECONDS * fps);
    ex = {
      width: width, height: height, fps: fps, audioRate: audioRate, hasAudio: !!aenc,
      muxer: muxer, target: target, venc: venc, aenc: aenc, fail: fail,
      cardFrames: cardFrames, cardIndex: 0, cardUs: us(cardFrames, fps),
      videoChunks: 0, frames: 0, samples: 0, scratch: new Uint8Array(width * height * 4), stash: [],
      last: new Uint8Array(width * height * 4), haveLast: false,
      cardCanvas: null, closing: false, failed: function () { return failed; },
      started: performance.now()
    };

    var cv = document.createElement('canvas');
    cv.width = width; cv.height = height;
    ex.cardCanvas = cv;

    ex.outroStart = null; ex.outroEnd = null;
    if (aenc) pushMusicOnly(0, CARD_SECONDS);   // under the title card, so audio starts at 0 like the video
    pumpCard();
    setStatus('Rendering clip…', 0, width + '×' + height + ' · ' + fps + ' fps · ' +
              cfg.video.config.codec + (aenc ? ' + ' + audioCfg.config.codec : ', no audio') +
              (cfg.video.muxer !== 'avc' ? '  (H.264 not used: ' + ((cfg.notes || []).filter(function (n) { return /^avc1/.test(n); })[0] || 'unavailable') + ')' : ''));
    return true;
  }

  // Level of the menu music `t` seconds into the file (0 = start of the title card).
  function musicGain(t) {
    if (t < CARD_SECONDS) {
      var fadeIn = Math.min(1, t / 0.4);
      var toDuck = Math.max(0, (t - (CARD_SECONDS - 0.8)) / 0.8);
      return fadeIn * (MUSIC_FULL + (MUSIC_DUCKED - MUSIC_FULL) * toDuck);
    }
    if (ex.outroStart != null && t >= ex.outroStart) {
      var up = Math.min(1, (t - ex.outroStart) / OUTRO_FADE_SECONDS);
      var g = MUSIC_DUCKED + (MUSIC_FULL - MUSIC_DUCKED) * up;
      var remaining = ex.outroEnd - t;
      return remaining < 1.5 ? g * Math.max(0, remaining / 1.5) : g;
    }
    return MUSIC_DUCKED;
  }

  // Adds the music to `data` (interleaved 16-bit stereo, or null for music only) for `n` frames starting
  // `start` samples into the track, and returns the samples.
  function mixMusic(data, start, n) {
    var out = data || new Int16Array(n * 2);
    if (!music) return out;
    var rate = ex.audioRate, ratio = music.rate / rate;
    for (var i = 0; i < n; i++) {
      var g = musicGain((start + i) / rate);
      if (g <= 0) continue;
      var pos = ((start + i) * ratio) % music.length;       // the music loops if the video is longer
      var k = Math.floor(pos), f = pos - k, k2 = (k + 1) % music.length;
      var l = music.left[k] + (music.left[k2] - music.left[k]) * f;
      var r = music.right[k] + (music.right[k2] - music.right[k]) * f;
      var a = out[i * 2] + l * g * 32767, b = out[i * 2 + 1] + r * g * 32767;
      out[i * 2] = a > 32767 ? 32767 : a < -32768 ? -32768 : a;
      out[i * 2 + 1] = b > 32767 ? 32767 : b < -32768 ? -32768 : b;
    }
    return out;
  }

  // Encode `seconds` of music without game audio starting `fromSamples` samples into the track
  // (the track's time zero is the start of the title card).
  function pushMusicOnly(fromSamples, seconds) {
    var rate = ex.audioRate, remaining = Math.round(seconds * rate), done = fromSamples;
    while (remaining > 0) {
      var n = Math.min(remaining, rate >> 2);
      ex.aenc.encode(new AudioData({ format: 's16', sampleRate: rate, numberOfFrames: n, numberOfChannels: 2,
                                      timestamp: Math.round(done * 1000000 / rate), data: mixMusic(null, done, n) }));
      done += n; remaining -= n;
    }
  }

  function pumpCard() {
    if (!ex || ex.failed()) return;
    var ctx = ex.cardCanvas.getContext('2d');
    while (ex.cardIndex < ex.cardFrames && ex.venc.encodeQueueSize < MAX_VIDEO_QUEUE) {
      drawCard(ctx, ex.width, ex.height, ex.cardIndex / ex.fps, CARD_SECONDS);
      var f = canvasFrame(ex.cardCanvas, us(ex.cardIndex, ex.fps), us(1, ex.fps));
      ex.venc.encode(f, encodeOpts(ex.cardIndex % KEYFRAME_EVERY === 0));
      f.close();
      ex.cardIndex++;
    }
    if (ex.cardIndex < ex.cardFrames) {
      setTimeout(pumpCard, 4);
    } else {
      // the engine's first tick(s) can land while the card is still being queued; they go in after it
      var held = ex.stash;
      ex.stash = [];
      held.forEach(function (h) { encodeGameFrame(h.data, h.idx); });
    }
  }

  function busy() {
    if (!ex || ex.closing) return false;
    if (ex.cardIndex < ex.cardFrames) return true;
    if (ex.venc.encodeQueueSize > MAX_VIDEO_QUEUE) return true;
    return !!(ex.aenc && ex.aenc.encodeQueueSize > MAX_AUDIO_QUEUE);
  }

  function encodeGameFrame(data, idx) {
    // a VideoFrame can't be built straight from the (shared) wasm heap
    ex.scratch.set(data);
    var f = rgbaFrame(ex.scratch, ex.width, ex.height, ex.cardUs + us(idx, ex.fps), us(1, ex.fps));
    ex.venc.encode(f, encodeOpts(idx % KEYFRAME_EVERY === 0));
    f.close();
  }

  function video(view, width, height) {
    if (!ex || ex.failed()) return;
    var idx = ex.frames++;
    ex.last.set(view);                 // the outro fades out from the final frame
    ex.haveLast = true;
    if (ex.cardIndex < ex.cardFrames) {
      ex.stash.push({ data: view.slice(), idx: idx });
    } else {
      encodeGameFrame(view, idx);
    }
    if ((ex.frames & 7) === 0) {
      var total = Math.max(1, info.clipSeconds ? info.clipSeconds * ex.fps : ex.frames + 1);
      var elapsed = (performance.now() - ex.started) / 1000;
      var frac = Math.min(0.95, ex.frames / total);
      var eta = frac > 0.03 ? Math.round(elapsed * (1 - frac) / frac) : null;
      setStatus('Rendering clip…', frac,
                Math.round(frac * 100) + '% · ' + elapsed.toFixed(0) + ' s elapsed' + (eta != null ? ' · about ' + eta + ' s left' : ''));
    }
  }

  function audio(view) {
    if (!ex || ex.failed() || !ex.aenc) return;
    var data = new Int16Array(view.byteLength >> 1);
    new Uint8Array(data.buffer).set(view);
    var n = data.length >> 1;
    var start = Math.round(CARD_SECONDS * ex.audioRate) + ex.samples;
    ex.aenc.encode(new AudioData({ format: 's16', sampleRate: ex.audioRate, numberOfFrames: n, numberOfChannels: 2,
                                    timestamp: ex.cardUs + Math.round(ex.samples * 1000000 / ex.audioRate),
                                    data: mixMusic(data, start, n) }));
    ex.samples += n;
  }

  // Fade the last game frame into the logo screen, then hold on it.
  async function encodeOutro(cur) {
    var w = cur.width, h = cur.height, fps = cur.fps;
    var fadeFrames = Math.round(OUTRO_FADE_SECONDS * fps), holdFrames = Math.round(OUTRO_HOLD_SECONDS * fps);

    var endCv = document.createElement('canvas');
    endCv.width = w; endCv.height = h;
    drawEndScreen(endCv.getContext('2d'), w, h);

    var lastCv = document.createElement('canvas');
    lastCv.width = w; lastCv.height = h;
    var out = document.createElement('canvas');
    out.width = w; out.height = h;
    var octx = out.getContext('2d');
    if (cur.haveLast) lastCv.getContext('2d').putImageData(new ImageData(new Uint8ClampedArray(cur.last.buffer), w, h), 0, 0);

    var first = cur.frames;
    if (cur.aenc) {
      cur.outroStart = CARD_SECONDS + cur.samples / cur.audioRate;
      cur.outroEnd = cur.outroStart + OUTRO_FADE_SECONDS + OUTRO_HOLD_SECONDS;
    }
    for (var i = 0; i < fadeFrames + holdFrames; i++) {
      while (cur.venc.encodeQueueSize >= MAX_VIDEO_QUEUE) await sleep(4);
      if (cur.failed()) return;
      var a = i < fadeFrames ? (i + 1) / fadeFrames : 1;
      if (a < 1 || i === fadeFrames - 1) {     // the held logo screen only needs drawing once
        octx.globalAlpha = 1;
        if (cur.haveLast && a < 1) {
          octx.drawImage(lastCv, 0, 0);
          octx.globalAlpha = a * a * (3 - 2 * a);
        }
        octx.drawImage(endCv, 0, 0);
      }
      var f = canvasFrame(out, cur.cardUs + us(first + i, fps), us(1, fps));
      cur.venc.encode(f, encodeOpts(i === 0));
      f.close();
      if ((i & 15) === 0) setStatus('Finishing…', 0.96 + 0.03 * i / (fadeFrames + holdFrames), '');
    }
    if (cur.aenc) {
      // the game's audio stops where its video does; the music carries the track through the outro
      pushMusicOnly(Math.round(CARD_SECONDS * cur.audioRate) + cur.samples, OUTRO_FADE_SECONDS + OUTRO_HOLD_SECONDS);
    }
  }

  async function finish(cur) {
    try {
      setStatus('Finishing…', 0.96, '');
      await encodeOutro(cur);
      setStatus('Finishing the file…', 0.99, '');
      await cur.venc.flush();
      if (cur.aenc) await cur.aenc.flush();
      var expected = cur.cardFrames + cur.frames + Math.round(OUTRO_FADE_SECONDS * cur.fps) + Math.round(OUTRO_HOLD_SECONDS * cur.fps);
      if (cur.failed()) return;
      if (cur.videoChunks < expected * 0.95) {
        // the encoder dropped frames (or the muxer refused them): don't hand over a file that plays black
        throw new Error('The video track is incomplete (' + cur.videoChunks + ' of ' + expected + ' frames were encoded). ' +
                        'This browser\u2019s video encoder is not usable for the export; try Chrome or Edge.');
      }
      cur.muxer.finalize();
      var blob = new Blob([cur.target.buffer], { type: 'video/mp4' });
      var stamp = (info.fileStem || 'potg').replace(/[^A-Za-z0-9_.-]+/g, '_');
      showDone(blob, stamp + '.mp4');
    } catch (e) {
      showError(e);
    }
  }

  function close() {
    var cur = ex;
    if (!cur || cur.closing) return;
    cur.closing = true;
    if (cur.failed()) return;
    finish(cur);
  }

  // ---- public ---------------------------------------------------------------------------

  window.S4ndReplayExport = {
    // opts: {player, map, when, clipSeconds, fileStem, width, height, fps}
    init: function (Module, opts) {
      info = opts;
      Module.s4ndExport = { open: open, video: video, audio: audio, close: close, busy: busy };
      makeUi();
      setStatus('Checking encoder support…', 0, '');
      var logoReady = loadLogo().then(function (cv) { logo = cv; showLogoInUi(); }, function (e) {
        console.warn('[export] logo not available:', e && e.message);
      });
      var musicReady = loadMusic().then(function (m) { music = m; }, function (e) {
        console.warn('[export] music not available:', e && e.message);
      });
      var shotReady = loadLevelshot(opts.mapRaw || '').then(function (b) { shot = b; }, function (e) {
        console.warn('[export] loading screen not available:', e && e.message);
      });
      return Promise.all([probe(opts.width, opts.height, opts.fps), logoReady, musicReady, shotReady]).then(function (r) {
        cfg = r[0];
        setStatus('Loading the replay…', 0, 'The clip renders as soon as it starts playing.');
      }).catch(function (e) { showError(e); throw e; });
    },
    setStatus: setStatus
  };
})();
