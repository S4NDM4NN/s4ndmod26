/*
 * WASM IO support code
 *
 * Based on the Return to Castle Wolfenstein GPL Source Code
 * Copyright (C) 1999-2010 id Software LLC, a ZeniMax Media company.
 *
 * Additional modifications Copyright (C) 2025 Gregory Maynard-Hoare
 *
 * For license details, see the COPYING.txt file included with this project.
 */

#include "wasm_io.h"

#include "../qcommon/q_shared.h"
#include "../qcommon/qcommon.h"
#include "../client/client.h"

#include <emscripten.h>

void wasm_init_fs(void)
{
	// Fetch from IDBFS in the background
	EM_ASM(
		Module.restore_busy = 1;
		FS.mkdir("/s4ndmod");

		// We must autoPersist data because QVM programs can handle saves
		FS.mount(IDBFS, {autoPersist: true}, "/s4ndmod");

		if (Module._debug) console.info("Loading data...");
		FS.syncfs(true, function(err) {
			if (err)
				console.warn("Failed to load data:", err);
			else if (Module._debug)
				console.info("Data loaded.");

			Module.restore_busy = 0;
		});
	);
}

int wasm_restore_busy(void)
{
	// Verify whether IDBFS restore is complete
	return EM_ASM_INT(
		return Module.restore_busy;
	);
}

void wasm_hide_console(void)
{
	// Hide the game console and show the canvas
	EM_ASM(
		if (typeof Module.hideConsole === 'function')
			Module.hideConsole();
	);
}

void wasm_show_console(void)
{
	// Show the game console and hide the canvas
	EM_ASM(
		if (typeof Module.showConsole === 'function')
			Module.showConsole();
	);
}

void wasm_set_connected(int connected)
{
	// Let the browser know whether a game session is active, so it can
	// warn before the tab is closed/reloaded (see beforeunload in shell.html)
	EM_ASM({
		if (typeof Module.setConnected === 'function')
			Module.setConnected($0);
	}, connected);
}

void wasm_export_file(char* filepath)
{
	// Prompt the user to optionally save a file to the host filesystem
	EM_ASM({
		if (typeof Module.exportFile === 'function')
			Module.exportFile(UTF8ToString($0));
	}, filepath);
}

/*
 * EM_JS avoids the C preprocessor comma-splitting bug:
 * [ ] brackets don't protect commas in macro argument parsing (only ( ) do),
 * so EM_ASM breaks when JS contains arrays-of-arrays.  EM_JS captures the
 * entire body as __VA_ARGS__ and sidesteps the issue entirely.
 */
EM_JS(void, js_ensure_paks, (), {
	Module.paks_ready = 0;

	var urls  = [];
	var dests = [];
	urls.push("/downloads/main/demopak0.pk3");       dests.push("/s4ndmod/main/");
	urls.push("/downloads/s4ndmod26/s4ndmod26.pk3"); dests.push("/s4ndmod/s4ndmod26/");

	try { FS.mkdir("/s4ndmod/main/"); }      catch(e) {}
	try { FS.mkdir("/s4ndmod/s4ndmod26/"); } catch(e) {}

	// The mod pk3 changes on every rebuild, so always re-download it.
	// Main paks are large and stable; skip them if already present.
	var always_refresh = { "/downloads/s4ndmod26/s4ndmod26.pk3": true };

	var needed_urls  = [];
	var needed_dests = [];
	for (var i = 0; i < urls.length; i++) {
		var name = urls[i].split("/").pop();
		if (always_refresh[urls[i]]) {
			needed_urls.push(urls[i]);
			needed_dests.push(dests[i]);
		} else {
			try { FS.stat(dests[i] + name); }
			catch(e) { needed_urls.push(urls[i]); needed_dests.push(dests[i]); }
		}
	}

	if (!needed_urls.length) { Module.paks_ready = 1; return; }

	var statusEl   = document.getElementById("status");
	var progressEl = document.getElementById("progress");
	var idx = 0;

	function next() {
		if (idx >= needed_urls.length) {
			if (statusEl) statusEl.innerHTML = "Saving game files...";
			FS.syncfs(false, function(err) {
				if (err) console.warn("pak syncfs:", err);
				if (progressEl) progressEl.hidden = true;
				if (statusEl)   statusEl.innerHTML = "Starting...";
				Module.paks_ready = 1;
			});
			return;
		}
		var url  = needed_urls[idx];
		var dest = needed_dests[idx];
		idx++;
		var name = url.split("/").pop();
		if (statusEl)   statusEl.innerHTML = "Downloading " + name + " (" + idx + "/" + needed_urls.length + ")...";
		if (progressEl) { progressEl.value = 0; progressEl.max = 100; progressEl.hidden = false; }

		fetch(url, { cache: "no-store" }).then(function(resp) {
			if (!resp.ok) throw new Error("HTTP " + resp.status);
			var total  = parseInt(resp.headers.get("content-length") || "0");
			var reader = resp.body.getReader();
			var chunks = [];
			var loaded = 0;

			function pump() {
				return reader.read().then(function(r) {
					if (r.done) {
						var buf = new Uint8Array(loaded);
						var off = 0;
						for (var j = 0; j < chunks.length; j++) {
							buf.set(chunks[j], off);
							off += chunks[j].length;
						}
						FS.writeFile(dest + name, buf);
						next();
						return;
					}
					chunks.push(r.value);
					loaded += r.value.length;
					if (total && progressEl)
						progressEl.value = Math.round(loaded / total * 100);
					return pump();
				});
			}
			return pump();
		}).catch(function(err) {
			console.error("pak download failed:", url, err);
			next();
		});
	}
	next();
});

EM_JS(void, js_begin_download, (const char *localNamePtr, const char *remoteNamePtr, const char *baseUrlPtr), {
	var localName = UTF8ToString(localNamePtr);
	var remoteName = UTF8ToString(remoteNamePtr);
	var baseUrl = UTF8ToString(baseUrlPtr);
	var statusEl = document.getElementById("status");
	var progressEl = document.getElementById("progress");

	function ensureDirTree(path) {
		var parts = path.split("/").filter(Boolean);
		var acc = "";
		for (var i = 0; i < parts.length; i++) {
			acc += "/" + parts[i];
			try { FS.mkdir(acc); } catch (e) {}
		}
	}

	function writeFile(destPath, bytes) {
		var slash = destPath.lastIndexOf("/");
		if (slash > 0) {
			ensureDirTree(destPath.slice(0, slash));
		}
		FS.writeFile(destPath, bytes);
	}

	function normalizeBase(url) {
		if (!url) {
			return "";
		}
		while (url.length > 0 && url.charAt(url.length - 1) === "/") {
			url = url.slice(0, -1);
		}
		return url;
	}

	var normalizedBase = normalizeBase(baseUrl);
	var url = normalizedBase ? (normalizedBase + "/" + remoteName) : remoteName;
	var destPath = "/s4ndmod/" + localName;

	Module.download_status = 1;

	if (statusEl) statusEl.innerHTML = "Downloading " + remoteName + "...";
	if (progressEl) {
		progressEl.value = 0;
		progressEl.max = 100;
		progressEl.hidden = false;
	}

	fetch(url, { cache: "no-store" }).then(function(resp) {
		if (!resp.ok) throw new Error("HTTP " + resp.status);
		var total = parseInt(resp.headers.get("content-length") || "0", 10);
		var reader = resp.body.getReader();
		var chunks = [];
		var loaded = 0;

		function pump() {
			return reader.read().then(function(r) {
				if (r.done) {
					var buf = new Uint8Array(loaded);
					var off = 0;
					for (var j = 0; j < chunks.length; j++) {
						buf.set(chunks[j], off);
						off += chunks[j].length;
					}
					writeFile(destPath, buf);
					FS.syncfs(false, function(err) {
						if (err) {
							console.error("download syncfs failed:", err);
							Module.download_status = -1;
						} else {
							if (progressEl) progressEl.hidden = true;
							if (statusEl) statusEl.innerHTML = "Download complete.";
							Module.download_status = 2;
						}
					});
					return;
				}
				chunks.push(r.value);
				loaded += r.value.length;
				if (total && progressEl) {
					progressEl.value = Math.round(loaded / total * 100);
				}
				return pump();
			});
		}

		return pump();
	}).catch(function(err) {
		console.error("WASM download failed:", url, err);
		Module.download_status = -1;
	});
});

void wasm_ensure_paks(void)
{
	js_ensure_paks();
}

int wasm_paks_ready(void)
{
	return EM_ASM_INT(return Module.paks_ready | 0;);
}

void wasm_begin_download(const char *localName, const char *remoteName, const char *baseUrl)
{
	js_begin_download(localName, remoteName, baseUrl);
}

int wasm_download_status(void)
{
	return EM_ASM_INT(return Module.download_status | 0;);
}

void wasm_vid_resize(void)
{
	// Notify JS after a resolution change
	EM_ASM(
		if (typeof Module.winResized === 'function')
			Module.winResized();
	);
}

void wasm_capture_mouse(void)
{
	// Ensure the pointer is captured in the canvas
	EM_ASM(
		if (typeof Module.captureMouse === 'function')
			Module.captureMouse();
	);
}

EMSCRIPTEN_KEEPALIVE
void wasm_set_cvar(const char *name, const char *value)
{
	Cvar_Set(name, value);
}

EMSCRIPTEN_KEEPALIVE
const char *wasm_get_cvar(const char *name)
{
	return Cvar_VariableString(name);
}

EMSCRIPTEN_KEEPALIVE
const char *wasm_get_raw_config(void)
{
	// Returns the actual on-disk wolfconfig_mp.cfg content (the file the
	// engine itself execs at boot via Com_ExecuteCfg()), not a synthesized
	// subset - lets the Raw Config tab show/edit the real thing. Static
	// buffer freed-then-reallocated each call rather than left to leak,
	// since this can be called repeatedly across a long session.
	static char *contents = NULL;
	void *data;
	long len;

	if (contents) {
		Z_Free(contents);
		contents = NULL;
	}

	len = FS_ReadFile(Q3CONFIG_CFG, &data);
	if (len < 0) {
		return "";
	}

	contents = Z_Malloc(len + 1);
	Com_Memcpy(contents, data, len);
	contents[len] = '\0';
	FS_FreeFile(data);
	return contents;
}

EMSCRIPTEN_KEEPALIVE
void wasm_set_raw_config(const char *text)
{
	// Written verbatim - the engine execs this file at boot the same as
	// any other startup cfg (see Com_ExecuteCfg()), so whatever's here
	// takes effect on the next reload with no argv/cvar plumbing needed.
	FS_WriteFile(Q3CONFIG_CFG, text, (int)strlen(text));
}

// K_PAD0_A..K_PAD0_TOUCHPAD is one contiguous block in keycodes.h (see the
// "Gamepad controls" comment there) - everything below it is keyboard/
// mouse, everything in it is gamepad. Used to keep the Keyboard Binds and
// Controller Binds tabs from finding/clobbering each other's bind for the
// same command (most actions are legitimately bound on both at once, e.g.
// +forward on both UPARROW and PAD0_LEFTSTICK_UP - see wasmjoy.cfg).
//
// K_JOY1/K_JOY2 are the one exception: legacy generic-joystick-button
// keycodes that sit earlier in the enum, alongside K_AUX*/K_WORLD_*/etc,
// not inside the K_PAD0_* block - but this project's default config binds
// them to +leanleft/+leanright (see IN_GamepadMove's trigger+modifier
// lean handling in sdl_input.c and "bind JOY1/JOY2" in wolfconfig_mp.cfg),
// so they're gamepad binds in practice. Without carving them out here,
// the Controller Binds tab could never find or clear a lean bind - it
// would always show as unbound, and rebinding would leave the old JOY1/
// JOY2 bind stuck alongside the new one instead of replacing it.
static int wasm_bind_ranges(int gamepadOnly, int spans[2][2])
{
	if (gamepadOnly) {
		spans[0][0] = K_JOY1;   spans[0][1] = K_JOY2 + 1;
		spans[1][0] = K_PAD0_A; spans[1][1] = K_PAD0_TOUCHPAD + 1;
	} else {
		spans[0][0] = 0;        spans[0][1] = K_JOY1;
		spans[1][0] = K_JOY2 + 1; spans[1][1] = K_PAD0_A;
	}
	return 2;
}

EMSCRIPTEN_KEEPALIVE
const char *wasm_get_bound_key(const char *command, int gamepadOnly)
{
	int i, s, nSpans, spans[2][2];

	nSpans = wasm_bind_ranges(gamepadOnly, spans);

	for (s = 0; s < nSpans; s++) {
		for (i = spans[s][0]; i < spans[s][1]; i++) {
			char *binding = Key_GetBinding(i);
			if (binding && binding[0] && !Q_stricmp(binding, command)) {
				return Key_KeynumToString(i, qfalse);
			}
		}
	}
	return "";
}

EMSCRIPTEN_KEEPALIVE
void wasm_rebind_command(const char *keyName, const char *command, int gamepadOnly)
{
	int i, s, nSpans, spans[2][2], newKeynum;

	nSpans = wasm_bind_ranges(gamepadOnly, spans);

	// Clear any existing key (within this input type's spans only - a
	// keyboard rebind must not touch the gamepad bind for the same
	// command, and vice versa) already bound to this command, so
	// rebinding doesn't leave two keys pointing at the same action.
	for (s = 0; s < nSpans; s++) {
		for (i = spans[s][0]; i < spans[s][1]; i++) {
			char *binding = Key_GetBinding(i);
			if (binding && binding[0] && !Q_stricmp(binding, command)) {
				Key_SetBinding(i, "");
			}
		}
	}

	newKeynum = Key_StringToKeynum((char *)keyName);
	if (newKeynum >= 0) {
		Key_SetBinding(newKeynum, command);
	}
}

// Not declared in qcommon.h - only ever called internally by common.c/
// cl_main.c on a throttle. We bypass that throttle for a one-off flush
// right when the user closes the settings panel, so a live-applied cvar
// (e.g. cg_fov) is durable on disk before any reconnect/map-load re-execs
// the persisted mod config over top of it.
void Com_WriteConfiguration( void );

EMSCRIPTEN_KEEPALIVE
void wasm_flush_config(void)
{
	// This only rewrites the in-memory (MEMFS-backed) copy of the file -
	// IDBFS's autoPersist push to IndexedDB is asynchronous and NOT done
	// by the time this returns. A location.reload() right after this call
	// can and does race ahead of that persist and lose the write (seen
	// directly: config.cfg read back after reload still had the old
	// value). JS must poll wasm_config_flush_busy() and wait for it to
	// clear before reloading/navigating - see the settings overlay's
	// Apply/Cancel handlers.
	Com_WriteConfiguration();
	EM_ASM(
		Module.config_flush_busy = 1;
		FS.syncfs(false, function(err) {
			if (err)
				console.warn("Failed to persist config:", err);
			else if (Module._debug)
				console.info("Config persisted.");

			Module.config_flush_busy = 0;
		});
	);
}

int wasm_config_flush_busy(void)
{
	// Verify whether the config persist to IDBFS is complete
	return EM_ASM_INT(
		return Module.config_flush_busy;
	);
}

EMSCRIPTEN_KEEPALIVE
void wasm_persist_fs(void)
{
	// Same IDBFS push (and Module.config_flush_busy tracking, reused by JS
	// via the existing wasm_config_flush_busy() poll) as wasm_flush_config()
	// above, but WITHOUT the Com_WriteConfiguration() call - that rewrites
	// wolfconfig_mp.cfg from the live cvars, which would immediately
	// clobber a hand-edit just written via wasm_set_raw_config().
	EM_ASM(
		Module.config_flush_busy = 1;
		FS.syncfs(false, function(err) {
			if (err)
				console.warn("Failed to persist config:", err);
			else if (Module._debug)
				console.info("Config persisted.");

			Module.config_flush_busy = 0;
		});
	);
}

// One-time migration for browser profiles that persisted their own
// wolfconfig_mp.cfg before gamepad support existed in the shipped
// template (see the "only ever seeds first-time defaults" comment at
// the top of wasm/fs/main/wolfconfig_mp.cfg) - without this, a
// returning player's old persisted config never gains the new
// defaults, and the controller does nothing until they manually type
// "exec controller.cfg". Gated by a marker cvar so a fully-migrated
// profile is cheap to no-op on every later boot, and it only ever
// fills in a gap - it never overwrites a bind the player already has,
// whether from a manual "exec controller.cfg" or their own rebinds.
//
// Only in_joystick/in_joystickUseAnalog and the PAD0_* binds actually
// need this: every other gamepad-related cvar (aim assist,
// controller curve/lean-mod, j_*_axis) already has a matching built-in
// default registered via its own Cvar_Get() elsewhere (sdl_input.c,
// cl_input.c, cl_main.c), so an old profile gets those for free
// regardless. in_joystick/in_joystickUseAnalog's own built-in defaults
// are "0" (off) though, and there's no such fallback for binds at all -
// those two are the actual reason the controller does nothing.
//
// Deliberately does NOT force a Com_WriteConfiguration()/syncfs here:
// this runs from Com_Init(), before most cvars this engine ever writes
// out are even registered yet, so a write this early would persist a
// badly incomplete config. Whatever this build's normal save path is
// (Settings Apply/Cancel, quit, etc.) picks these changes up same as
// any other cvar/bind change; until then this just harmlessly re-fills
// the same gaps on the next boot, which is still the desired behavior.
void wasm_migrate_joystick_defaults(void)
{
	static const struct { const char *key; const char *command; } binds[] = {
		{ "PAD0_LEFTSTICK_LEFT",   "+moveleft" },
		{ "PAD0_LEFTSTICK_RIGHT",  "+moveright" },
		{ "PAD0_LEFTSTICK_UP",     "+forward" },
		{ "PAD0_LEFTSTICK_DOWN",   "+back" },
		{ "PAD0_RIGHTSTICK_LEFT",  "+left" },
		{ "PAD0_RIGHTSTICK_RIGHT", "+right" },
		{ "PAD0_RIGHTSTICK_UP",    "+lookup" },
		{ "PAD0_RIGHTSTICK_DOWN",  "+lookdown" },
		{ "PAD0_A",                "+moveup" },
		{ "PAD0_B",                "+movedown" },
		{ "PAD0_X",                "+reload" },
		{ "PAD0_Y",                "+scores" },
		{ "PAD0_LEFTSHOULDER",     "weapprev" },
		{ "PAD0_RIGHTSHOULDER",    "weapnext" },
		{ "PAD0_LEFTTRIGGER",      "+speed" },
		{ "PAD0_RIGHTTRIGGER",     "+attack" },
		{ "PAD0_LEFTSTICK_CLICK",  "+sprint" },
		{ "PAD0_DPAD_UP",          "+activate" },
		{ "PAD0_DPAD_DOWN",        "+zoom" },
		{ "PAD0_DPAD_LEFT",        "+leanleft" },
		{ "PAD0_DPAD_RIGHT",       "+leanright" },
		{ "PAD0_BACK",             "OpenLimboMenu" },
		{ "PAD0_START",            "togglemenu" },
	};
	cvar_t *marker;
	int i;

	marker = Cvar_Get( "in_joystickDefaultsApplied", "0", CVAR_ARCHIVE );
	if ( marker->integer )
		return;

	Cvar_Set( "in_joystick", "1" );
	Cvar_Set( "in_joystickUseAnalog", "1" );

	for ( i = 0; i < (int)(sizeof(binds) / sizeof(binds[0])); i++ ) {
		int keynum = Key_StringToKeynum( (char *)binds[i].key );
		char *existing;

		if ( keynum < 0 )
			continue;

		existing = Key_GetBinding( keynum );
		if ( !existing || !existing[0] )
			Key_SetBinding( keynum, binds[i].command );
	}

	Cvar_Set( "in_joystickDefaultsApplied", "1" );
}
