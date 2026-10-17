Baked bitmap fonts for the optional text font (cg_s4ndfont, see mod/src/cgame/cg_s4ndfont.c).  Made with tools/local/bakefont.py.

cg_s4ndfont 1   fontImage_30.dat, fontImage_0_30.tga, fontImage_1_30.tga
  Courier Prime Bold at 30 pt.  Copyright 2015 The Courier Prime Project Authors
  (https://github.com/quoteunquoteapps/CourierPrime), SIL Open Font License 1.1, see LICENSE-CourierPrime-OFL.txt.

cg_s4ndfont 2   fontImage_28.dat, fontImage_0_28.tga, fontImage_1_28.tga
  DejaVu Sans Mono Bold at 28 pt.  Free to redistribute under LICENSE-DejaVu.txt (derived from Bitstream Vera).

cg_s4ndfont 0 is the game's own font (default).

The engine looks fonts up by point size only (fonts/fontImage_<size>.dat), so the size is the font's identity.  Sizes 12,
16, 17, 20 and 24 belong to the game's menus; do not reuse them.

Only add fonts that are free to redistribute.  Microsoft fonts (Courier New, Arial Black) are not: to try one locally, bake
it from your own copy with tools/local/bakefont.py over an existing size and keep the result out of git.
