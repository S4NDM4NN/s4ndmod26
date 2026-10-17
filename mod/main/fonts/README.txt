Baked bitmap fonts for the font prototype (cg_fontTest, see mod/src/cgame/cg_main.c).

fontImage_28.dat, fontImage_0_28.tga, fontImage_1_28.tga
  DejaVu Sans Mono Bold rendered at 28 pt with tools/local/bakefont.py.  DejaVu fonts are free to redistribute under the
  licence in LICENSE-DejaVu.txt (derived from Bitstream Vera).

The engine looks fonts up by point size only (fonts/fontImage_<size>.dat), so the size is the font's identity.  Sizes 12,
16, 17, 20 and 24 belong to the game's menus; do not reuse them.

Do not add fonts that are not free to redistribute (for example Microsoft's Courier New or Arial Black).  To try those
locally, bake them from your own copy with tools/local/bakefont.py and put the output in your local pk3 only.
