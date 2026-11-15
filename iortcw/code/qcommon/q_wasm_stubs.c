/*
 * Standalone exports of math/string symbols for WASM dynamic linking.
 *
 * cgame.mp.wasm and ui.mp.wasm (SIDE_MODULE=1) import these from env (the main
 * module).  They cannot be added to q_math.c because q_shared.h already
 * defines static-inline versions of the same names, which would cause a
 * static vs. external linkage conflict in the same translation unit.
 *
 * This file is intentionally minimal: it does NOT include q_shared.h.
 */

#ifdef __EMSCRIPTEN__

#include <math.h>
#include <string.h>

typedef float vec_t;
typedef vec_t vec3_t[3];

#define VecSub(a,b,c) ((c)[0]=(a)[0]-(b)[0],(c)[1]=(a)[1]-(b)[1],(c)[2]=(a)[2]-(b)[2])
#define VecAdd(a,b,c) ((c)[0]=(a)[0]+(b)[0],(c)[1]=(a)[1]+(b)[1],(c)[2]=(a)[2]+(b)[2])

int VectorCompare( const vec3_t v1, const vec3_t v2 ) {
	if ( v1[0] != v2[0] || v1[1] != v2[1] || v1[2] != v2[2] ) return 0;
	return 1;
}

vec_t VectorLength( const vec3_t v ) {
	return sqrtf( v[0]*v[0] + v[1]*v[1] + v[2]*v[2] );
}

vec_t VectorLengthSquared( const vec3_t v ) {
	return v[0]*v[0] + v[1]*v[1] + v[2]*v[2];
}

void CrossProduct( const vec3_t v1, const vec3_t v2, vec3_t cross ) {
	cross[0] = v1[1]*v2[2] - v1[2]*v2[1];
	cross[1] = v1[2]*v2[0] - v1[0]*v2[2];
	cross[2] = v1[0]*v2[1] - v1[1]*v2[0];
}

void VectorInverse( vec3_t v ) {
	v[0] = -v[0]; v[1] = -v[1]; v[2] = -v[2];
}

vec_t Distance( const vec3_t p1, const vec3_t p2 ) {
	vec3_t v;
	VecSub(p2, p1, v);
	return sqrtf( v[0]*v[0] + v[1]*v[1] + v[2]*v[2] );
}

vec_t DistanceSquared( const vec3_t p1, const vec3_t p2 ) {
	vec3_t v;
	VecSub(p2, p1, v);
	return v[0]*v[0] + v[1]*v[1] + v[2]*v[2];
}

unsigned int AngleNormalizeInt( int angle ) {
	if ( angle < 0 )
		angle = 65536 + ( angle % 65536 );
	return (unsigned int)( angle % 65536 );
}

void RotatePointAroundVertex( vec3_t pnt, float rot_x, float rot_y, float rot_z, const vec3_t origin ) {
	float tmp[11];
	VecSub(pnt, origin, pnt);
	tmp[0] = sinf(rot_x); tmp[1] = cosf(rot_x);
	tmp[2] = sinf(rot_y); tmp[3] = cosf(rot_y);
	tmp[4] = sinf(rot_z); tmp[5] = cosf(rot_z);
	tmp[6]  = pnt[1]*tmp[5]; tmp[7]  = pnt[0]*tmp[4];
	tmp[8]  = pnt[0]*tmp[5]; tmp[9]  = pnt[1]*tmp[4];
	tmp[10] = pnt[2]*tmp[3];
	pnt[0] = tmp[3]*(tmp[8]-tmp[9]) + tmp[3]*tmp[2];
	pnt[1] = tmp[0]*(tmp[2]*tmp[8]-tmp[2]*tmp[9]-tmp[10]) + tmp[1]*(tmp[7]+tmp[6]);
	pnt[2] = tmp[1]*(-tmp[2]*tmp[8]+tmp[2]*tmp[9]+tmp[10]) + tmp[0]*(tmp[7]+tmp[6]);
	VecAdd(pnt, origin, pnt);
}

void COM_StripExtensionSafe( const char *in, char *out, int destsize ) {
	const char *dot, *slash;
	int n;
	if ( !in || !out || destsize < 1 ) return;
	dot   = strrchr(in, '.');
	slash = strrchr(in, '/');
	if ( dot && ( !slash || slash < dot ) ) {
		n = (int)(dot - in);
		if ( n >= destsize ) n = destsize - 1;
		if ( in != out && n > 0 ) memmove(out, in, n);
		out[n] = '\0';
		return;
	}
	if ( in != out ) {
		strncpy(out, in, (size_t)(destsize - 1));
		out[destsize - 1] = '\0';
	} else {
		out[destsize - 1] = '\0';
	}
}

char *Q_strrchr( const char *string, int c ) {
	char cc = (char)c, *s = (char *)string, *sp = NULL;
	while ( *s ) {
		if ( *s == cc ) sp = s;
		s++;
	}
	if ( cc == 0 ) sp = s;
	return sp;
}

#endif /* __EMSCRIPTEN__ */
