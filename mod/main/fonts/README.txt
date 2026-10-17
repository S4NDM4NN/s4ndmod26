Baked bitmap fonts for the font prototype (cg_fontTest, see mod/src/cgame/cg_main.c).  Made with tools/local/bakefont.py.

fontImage_28.dat, fontImage_0_28.tga, fontImage_1_28.tga   (cg_fontTest 3)
  DejaVu Sans Mono Bold at 28 pt.  Free to redistribute under LICENSE-DejaVu.txt (derived from Bitstream Vera).

fontImage_30.dat, fontImage_0_30.tga, fontImage_1_30.tga   (cg_fontTest 2)
  Courier Prime Bold at 30 pt: a free Courier-style typewriter face, used in place of Courier New Bold.
  Copyright 2015 The Courier Prime Project Authors (https://github.com/quoteunquoteapps/CourierPrime), SIL Open Font
  License 1.1, see LICENSE-CourierPrime-OFL.txt.

The engine looks fonts up by point size only (fonts/fontImage_<size>.dat), so the size is the font's identity.  Sizes 12,
16, 17, 20 and 24 belong to the game's menus; do not reuse them.

Do not add fonts that are not free to redistribute (for example Microsoft's Courier New or Arial Black).  To try those
locally, bake them from your own copy with tools/local/bakefont.py and put the output in your local pk3 only.  cg_fontTest 4
(Arial Black) uses size 27 and has no shipped font; cg_fontTest 2 can be pointed at the real Courier New Bold by baking it
over size 30 locally (do not commit that).
