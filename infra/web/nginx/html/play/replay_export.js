/*
 * MP4 export of a play-of-the-game replay.
 *
 * In export mode (/play/?replay=NAME&export=1) the engine's video recorder (cl_avi.c) runs for
 * the playback part of the clip with a fixed timestep: every engine tick is exactly one video
 * frame, however long it took to draw.  Instead of writing an AVI it hands each RGBA frame and
 * each chunk of 16-bit stereo PCM to Module.s4ndExport, defined here.  Frames go through
 * WebCodecs (H.264, or VP9 where H.264 can't be encoded) and audio through AAC (or Opus), and
 * mp4-muxer assembles the file.  A short title card is rendered in front of the clip.
 *
 * The engine skips a tick while busy() is true, so a slow encoder just slows the render down.
 */
(function () {
  'use strict';

  var CARD_SECONDS = 3;
  var VIDEO_BITRATE = 24000000;
  var KEYFRAME_EVERY = 120;
  var MAX_VIDEO_QUEUE = 6;
  var MAX_AUDIO_QUEUE = 40;

  var info = {};          // title card text and expected clip length, set by init()
  var ui = null;
  var cfg = null;         // codec choice, found by probe()
  var ex = null;          // the running export

  function us(frameIndex, fps) { return Math.round(frameIndex * 1000000 / fps); }

  // ---- codec selection ------------------------------------------------------------------

  var H264 = ['avc1.64002A', 'avc1.640028', 'avc1.4D402A', 'avc1.42002A'];   // High/Main/Baseline, level 4.2
  var VP9 = ['vp09.00.41.08', 'vp09.00.40.08'];

  async function pickVideo(width, height, fps) {
    var tries = H264.map(function (c) { return { muxer: 'avc', codec: c, extra: { avc: { format: 'avc' } } }; })
      .concat(VP9.map(function (c) { return { muxer: 'vp9', codec: c, extra: {} }; }));
    for (var i = 0; i < tries.length; i++) {
      var t = tries[i];
      var conf = Object.assign({ codec: t.codec, width: width, height: height, framerate: fps,
                                 bitrate: VIDEO_BITRATE, latencyMode: 'quality' }, t.extra);
      try {
        var r = await VideoEncoder.isConfigSupported(conf);
        if (r && r.supported) return { muxer: t.muxer, config: r.config || conf };
      } catch (e) { /* try the next one */ }
    }
    return null;
  }

  async function pickAudio(rate) {
    var tries = [{ muxer: 'aac', codec: 'mp4a.40.2' }, { muxer: 'opus', codec: 'opus' }];
    for (var i = 0; i < tries.length; i++) {
      var conf = { codec: tries[i].codec, sampleRate: rate, numberOfChannels: 2, bitrate: 192000 };
      try {
        var r = await AudioEncoder.isConfigSupported(conf);
        if (r && r.supported) return { muxer: tries[i].muxer, config: r.config || conf };
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
    var video = await pickVideo(width, height, fps);
    if (!video) throw new Error('This browser cannot encode ' + width + '×' + height + ' video.');
    var audio = {};
    for (var rate of [48000, 44100, 22050]) audio[rate] = await pickAudio(rate);
    return { video: video, audio: audio, width: width, height: height, fps: fps };
  }

  // ---- title card -----------------------------------------------------------------------

  function drawCard(ctx, w, h, t, total) {
    var fade = Math.max(0, Math.min(1, t / 0.5, (total - t) / 0.5));
    var g = ctx.createRadialGradient(w / 2, h / 2, h * 0.1, w / 2, h / 2, h * 0.9);
    g.addColorStop(0, '#2a2f38');
    g.addColorStop(1, '#07090c');
    ctx.globalAlpha = 1;
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, w, h);

    ctx.globalAlpha = fade;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    var font = function (px, weight) { return (weight || '700') + ' ' + px + 'px "Segoe UI", "Helvetica Neue", Arial, sans-serif'; };

    ctx.fillStyle = '#c8a24a';
    ctx.font = font(h * 0.045, '600');
    ctx.fillText('P L A Y   O F   T H E   G A M E', w / 2, h * 0.36);

    ctx.fillStyle = '#ffffff';
    ctx.font = font(h * 0.14, '800');
    var name = info.player || 'Unknown';
    // shrink long names to fit
    var size = h * 0.14;
    while (ctx.measureText(name).width > w * 0.86 && size > 20) { size -= 4; ctx.font = font(size, '800'); }
    ctx.fillText(name, w / 2, h * 0.5);

    ctx.fillStyle = '#9aa4b2';
    ctx.font = font(h * 0.04, '500');
    var line = [info.map, info.when].filter(Boolean).join('   ·   ');
    if (line) ctx.fillText(line, w / 2, h * 0.64);

    ctx.fillStyle = 'rgba(200,162,74,0.9)';
    ctx.fillRect(w * 0.38, h * 0.575, w * 0.24, 3);

    ctx.fillStyle = '#5d6674';
    ctx.font = font(h * 0.028, '600');
    ctx.fillText('S4NDMoD', w / 2, h * 0.93);
    ctx.globalAlpha = 1;
  }

  // ---- progress overlay -----------------------------------------------------------------

  function makeUi() {
    if (ui) return ui;
    var el = document.createElement('div');
    el.id = 'exportOverlay';
    el.style.cssText = 'position:fixed;left:0;right:0;bottom:0;z-index:50;padding:14px 16px 16px;' +
      'background:rgba(10,12,16,.92);color:#e6e9ee;font:14px/1.4 system-ui,sans-serif;' +
      'display:flex;flex-direction:column;gap:8px;border-top:1px solid #2b313b';
    el.innerHTML =
      '<div id="exportText" style="font-weight:600">Preparing export…</div>' +
      '<div style="height:8px;border-radius:4px;background:#252b35;overflow:hidden">' +
      '<div id="exportBar" style="height:100%;width:0;background:#c8a24a;transition:width .2s"></div></div>' +
      '<div id="exportSub" style="color:#9aa4b2;font-size:12px"></div>' +
      '<div id="exportActions" style="display:none;gap:10px"></div>';
    document.body.appendChild(el);
    ui = { el: el, text: el.querySelector('#exportText'), bar: el.querySelector('#exportBar'),
           sub: el.querySelector('#exportSub'), actions: el.querySelector('#exportActions') };
    return ui;
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
    a.style.cssText = 'background:#c8a24a;color:#111;padding:8px 18px;border-radius:6px;font-weight:700;text-decoration:none';
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
      firstTimestampBehavior: 'strict'
    };
    if (audioCfg) muxerOpts.audio = { codec: audioCfg.muxer, numberOfChannels: 2, sampleRate: audioRate };
    var muxer = new Mp4Muxer.Muxer(muxerOpts);

    var failed = false;
    function fail(e) { if (!failed) { failed = true; showError(e); } }

    var venc = new VideoEncoder({
      output: function (chunk, meta) { muxer.addVideoChunk(chunk, meta); },
      error: fail
    });
    venc.configure(cfg.video.config);

    var aenc = null;
    if (audioCfg) {
      aenc = new AudioEncoder({
        output: function (chunk, meta) { muxer.addAudioChunk(chunk, meta); },
        error: fail
      });
      aenc.configure(audioCfg.config);
    }

    var cardFrames = Math.round(CARD_SECONDS * fps);
    ex = {
      width: width, height: height, fps: fps, audioRate: audioRate, hasAudio: !!aenc,
      muxer: muxer, target: target, venc: venc, aenc: aenc, fail: fail,
      cardFrames: cardFrames, cardIndex: 0, cardUs: us(cardFrames, fps),
      frames: 0, samples: 0, scratch: new Uint8Array(width * height * 4), stash: [],
      cardCanvas: null, closing: false, failed: function () { return failed; },
      started: performance.now()
    };

    var cv = document.createElement('canvas');
    cv.width = width; cv.height = height;
    ex.cardCanvas = cv;

    // silence under the title card so the audio track starts at 0 like the video
    if (aenc) {
      var remaining = Math.round(CARD_SECONDS * audioRate), done = 0;
      while (remaining > 0) {
        var n = Math.min(remaining, audioRate);
        aenc.encode(new AudioData({ format: 's16', sampleRate: audioRate, numberOfFrames: n, numberOfChannels: 2,
                                    timestamp: Math.round(done * 1000000 / audioRate), data: new Int16Array(n * 2) }));
        done += n; remaining -= n;
      }
    }
    pumpCard();
    setStatus('Rendering clip…', 0, width + '×' + height + ' · ' + fps + ' fps · ' +
              cfg.video.config.codec + (aenc ? ' + ' + audioCfg.config.codec : ', no audio'));
    return true;
  }

  function pumpCard() {
    if (!ex || ex.failed()) return;
    var ctx = ex.cardCanvas.getContext('2d');
    while (ex.cardIndex < ex.cardFrames && ex.venc.encodeQueueSize < MAX_VIDEO_QUEUE) {
      drawCard(ctx, ex.width, ex.height, ex.cardIndex / ex.fps, CARD_SECONDS);
      var f = new VideoFrame(ex.cardCanvas, { timestamp: us(ex.cardIndex, ex.fps) });
      ex.venc.encode(f, { keyFrame: ex.cardIndex % KEYFRAME_EVERY === 0 });
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
    var f = new VideoFrame(ex.scratch, { format: 'RGBA', codedWidth: ex.width, codedHeight: ex.height,
                                          timestamp: ex.cardUs + us(idx, ex.fps), duration: us(1, ex.fps) });
    ex.venc.encode(f, { keyFrame: idx % KEYFRAME_EVERY === 0 });
    f.close();
  }

  function video(view, width, height) {
    if (!ex || ex.failed()) return;
    var idx = ex.frames++;
    if (ex.cardIndex < ex.cardFrames) {
      ex.stash.push({ data: view.slice(), idx: idx });
    } else {
      encodeGameFrame(view, idx);
    }
    if ((ex.frames & 7) === 0) {
      var total = Math.max(1, info.clipSeconds ? info.clipSeconds * ex.fps : ex.frames + 1);
      var elapsed = (performance.now() - ex.started) / 1000;
      setStatus('Rendering clip…', Math.min(0.97, ex.frames / total),
                ex.frames + ' frames (' + (ex.frames / ex.fps).toFixed(1) + ' s of video) · ' + elapsed.toFixed(0) + ' s elapsed');
    }
  }

  function audio(view) {
    if (!ex || ex.failed() || !ex.aenc) return;
    var data = new Int16Array(view.byteLength >> 1);
    new Uint8Array(data.buffer).set(view);
    var n = data.length >> 1;
    ex.aenc.encode(new AudioData({ format: 's16', sampleRate: ex.audioRate, numberOfFrames: n, numberOfChannels: 2,
                                    timestamp: ex.cardUs + Math.round(ex.samples * 1000000 / ex.audioRate), data: data }));
    ex.samples += n;
  }

  async function finish(cur) {
    try {
      setStatus('Finishing the file…', 0.98, '');
      await cur.venc.flush();
      if (cur.aenc) await cur.aenc.flush();
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
      return probe(opts.width, opts.height, opts.fps).then(function (c) {
        cfg = c;
        setStatus('Loading the replay…', 0, 'The clip renders as soon as it starts playing.');
      }).catch(function (e) { showError(e); throw e; });
    },
    setStatus: setStatus
  };
})();
