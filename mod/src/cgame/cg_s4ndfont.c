/*
===========================================================================
Optional text font (cg_s4ndfont)

The game draws most of its text (scoreboard, goals panel, kill feed, chat, centre messages, titles) with a bitmap
charset.  cg_s4ndfont draws that text with a baked font instead:

    cg_s4ndfont        0 = the game's own font (default)   1 = Courier Prime Bold   2 = DejaVu Sans Mono Bold
    cg_s4ndfontSize    capital height as a percent of the caller's character cell height (default 60)
    cg_s4ndfontSpace   letter spacing: 0 = the font's own width, 100 = the game font's cell width (default 30)

The fonts are bitmaps in fonts/fontImage_<size>.dat (see mod/main/fonts/README.txt).  The engine looks fonts up by
point size only, so the size is the font's identity: 30 = Courier Prime Bold, 28 = DejaVu Sans Mono Bold.  Only the
selected font is registered; if it is not available (missing files, or an engine with no free font slot) the game's own
font is used.

Callers that centre or right-align text, or count how many characters fit, work from charWidth * character count.
CG_S4ndFont_Advance() and CG_S4ndFont_StrWidth() give them the width the text really takes; with the option off they
return the original width, so nothing changes.
===========================================================================
*/

#include "cg_local.h"

enum { SF_COURIER, SF_DEJAVU, SF_NUM };

static struct {
	const char *name;
	int size;
	fontInfo_t font;
	qboolean tried;
} sfFonts[SF_NUM] = {
	{ "courierprime", 30 },
	{ "dejavumono",   28 },
};

// the selected font, registered the first time it is needed; NULL when off or not available
static fontInfo_t *SF_Current( void ) {
	int i = cg_s4ndfont.integer - 1;

	if ( i < 0 || i >= SF_NUM ) {
		return NULL;
	}
	if ( !sfFonts[i].tried ) {
		sfFonts[i].tried = qtrue;
		trap_R_RegisterFont( sfFonts[i].name, sfFonts[i].size, &sfFonts[i].font );
	}
	return sfFonts[i].font.glyphScale > 0 ? &sfFonts[i].font : NULL;
}

// For one character cell (cw x ch, text row at y): the glyph scale, the baseline, the advance per character, and the
// offset that centres a glyph in that advance.  NULL when the option is off.
static fontInfo_t *SF_Metrics( int cw, int ch, float y, float *scale, float *base, float *adv, float *off ) {
	fontInfo_t *f = SF_Current();
	float capUnits, nat;

	if ( !f ) {
		return NULL;
	}

	// Size from the cell height, not just its width: capital height = cg_s4ndfontSize percent of charHeight, and never
	// wider than one cell advance.
	capUnits = f->glyphs['H'].height * f->glyphScale;
	*scale = ( ch * cg_s4ndfontSize.value * 0.01f ) / capUnits;
	nat = f->glyphs['0'].xSkip * f->glyphScale;
	if ( *scale * nat > cw ) {
		*scale = cw / nat;
	}
	*base = y + ( ch + capUnits * *scale ) * 0.5f;     // centre the capitals in the cell

	// between the font's natural width (cg_s4ndfontSpace 0) and the cell width (100), glyph centred in the space
	nat *= *scale;
	*adv = nat + ( cw - nat ) * cg_s4ndfontSpace.value * 0.01f;
	*off = ( *adv - nat ) * 0.5f;
	return f;
}

// The advance one character really takes for a cw x ch cell (cw when the option is off).
float CG_S4ndFont_Advance( int cw, int ch ) {
	float scale, base, adv, off;

	if ( !SF_Metrics( cw, ch, 0, &scale, &base, &adv, &off ) ) {
		return cw;
	}
	return adv;
}

// Width a string takes: visible characters times the real advance.  For centring and right-aligning.
float CG_S4ndFont_StrWidth( const char *s, int cw, int ch ) {
	return CG_DrawStrlen( s ) * CG_S4ndFont_Advance( cw, ch );
}

// Called from CG_DrawStringExt / CG_DrawStringExt2.  Returns qfalse when the normal charset path should be used.
qboolean CG_S4ndFont_DrawString( int x, int y, const char *string, const float *setColor, qboolean forceColor, qboolean shadow, int cw, int ch, int maxChars ) {
	fontInfo_t *f;
	vec4_t color, black;
	const char *s;
	float xx, base, scale, adv, off;
	int cnt, pass;
	char tmp[2];

	f = SF_Metrics( cw, ch, y, &scale, &base, &adv, &off );
	if ( !f ) {
		return qfalse;
	}
	if ( maxChars <= 0 ) {
		maxChars = 32767;
	}

	black[0] = black[1] = black[2] = 0;
	black[3] = setColor[3];

	// pass 0: drop shadow (when asked for), pass 1: the text
	for ( pass = shadow ? 0 : 1; pass < 2; pass++ ) {
		s = string;
		xx = x + ( pass == 0 ? 1 : 0 );
		cnt = 0;
		Vector4Copy( setColor, color );
		while ( *s && cnt < maxChars ) {
			if ( Q_IsColorString( s ) ) {
				if ( !forceColor ) {
					memcpy( color, g_color_table[ColorIndex( *( s + 1 ) )], sizeof( color ) );
					color[3] = setColor[3];
				}
				s += 2;
				continue;
			}
			tmp[0] = *s;
			tmp[1] = 0;
			CG_Text_Paint_Ext( xx + off, base + ( pass == 0 ? 1 : 0 ), scale, scale, pass == 0 ? black : color, tmp, 0, 0, 0, f );
			xx += adv;
			cnt++;
			s++;
		}
	}
	return qtrue;
}
