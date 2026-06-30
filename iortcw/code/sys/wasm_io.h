/*
 * WASM IO support headers
 *
 * Based on the Return to Castle Wolfenstein GPL Source Code
 * Copyright (C) 1999-2010 id Software LLC, a ZeniMax Media company.
 *
 * Additional modifications Copyright (C) 2025 Gregory Maynard-Hoare
 *
 * For license details, see the COPYING.txt file included with this project.
 */

#ifndef __WASM_IO__
#define __WASM_IO__

void wasm_init_fs(void);
int wasm_restore_busy(void);
void wasm_ensure_paks(void);
int wasm_paks_ready(void);
void wasm_begin_download(const char *localName, const char *remoteName, const char *baseUrl);
int wasm_download_status(void);
void wasm_hide_console(void);
void wasm_show_console(void);
void wasm_export_file(char* filepath);
void wasm_vid_resize(void);
void wasm_capture_mouse(void);

#endif // !__WASM_IO__
