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

#include <emscripten.h>

void wasm_init_fs(void)
{
	// Fetch from IDBFS in the background
	EM_ASM(
		Module.restore_busy = 1;
		FS.mkdir("/s4ndmod");

		// We must autoPersist data because QVM programs can handle saves
		FS.mount(IDBFS, {autoPersist: true}, "/s4ndmod");

		console.info("Loading data...");
		FS.syncfs(true, function(err) {
			if (err)
				console.warn("Failed to load data:", err);
			else
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
	urls.push("/downloads/main/pak0.pk3");          dests.push("/s4ndmod/main/");
	urls.push("/downloads/main/axis_complex.pk3");   dests.push("/s4ndmod/main/");
	urls.push("/downloads/main/mp_pak0.pk3");        dests.push("/s4ndmod/main/");
	urls.push("/downloads/main/mp_pak1.pk3");        dests.push("/s4ndmod/main/");
	urls.push("/downloads/main/mp_pak2.pk3");        dests.push("/s4ndmod/main/");
	urls.push("/downloads/main/mp_pak3.pk3");        dests.push("/s4ndmod/main/");
	urls.push("/downloads/main/mp_pak4.pk3");        dests.push("/s4ndmod/main/");
	urls.push("/downloads/main/mp_pak5.pk3");        dests.push("/s4ndmod/main/");
	urls.push("/downloads/s4ndmod26/s4ndmod26.pk3"); dests.push("/s4ndmod/s4ndmod26/");

	try { FS.mkdir("/s4ndmod/main/"); }      catch(e) {}
	try { FS.mkdir("/s4ndmod/s4ndmod26/"); } catch(e) {}

	var needed_urls  = [];
	var needed_dests = [];
	for (var i = 0; i < urls.length; i++) {
		var name = urls[i].split("/").pop();
		try { FS.stat(dests[i] + name); }
		catch(e) { needed_urls.push(urls[i]); needed_dests.push(dests[i]); }
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

		fetch(url).then(function(resp) {
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

void wasm_ensure_paks(void)
{
	js_ensure_paks();
}

int wasm_paks_ready(void)
{
	return EM_ASM_INT(return Module.paks_ready | 0;);
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
