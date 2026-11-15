package replay

import (
	"encoding/binary"
	"math"
	"testing"
)

func putFloat32LE(b []byte, off int, v float32) {
	binary.LittleEndian.PutUint32(b[off:off+4], math.Float32bits(v))
}

func TestLookupSampleLayout(t *testing.T) {
	for _, sampleSize := range []int32{380, 388} {
		layout, ok := lookupSampleLayout(sampleSize)
		if !ok {
			t.Fatalf("expected known layout for sample size %d", sampleSize)
		}
		if layout.sampleSize != sampleSize {
			t.Fatalf("layout sample size mismatch: got %d want %d", layout.sampleSize, sampleSize)
		}
	}
	if _, ok := lookupSampleLayout(999); ok {
		t.Fatal("unexpected layout for unknown sample size")
	}
}

func TestParseSampleCurrentLayoutExtractsWeaponAndVectors(t *testing.T) {
	layout, ok := lookupSampleLayout(388)
	if !ok {
		t.Fatal("missing 388-byte sample layout")
	}

	buf := make([]byte, layout.sampleSize)
	binary.LittleEndian.PutUint32(buf[offClientNum:offClientNum+4], uint32(7))
	binary.LittleEndian.PutUint32(buf[offHealth:offHealth+4], uint32(93))
	binary.LittleEndian.PutUint32(buf[offTeam:offTeam+4], uint32(2))
	binary.LittleEndian.PutUint32(buf[offPmType:offPmType+4], uint32(1))
	binary.LittleEndian.PutUint32(buf[offPmFlags:offPmFlags+4], uint32(0x4000))
	binary.LittleEndian.PutUint32(buf[offWeaponstate:offWeaponstate+4], uint32(5))
	binary.LittleEndian.PutUint32(buf[layout.offWeapon:layout.offWeapon+4], uint32(14))
	putFloat32LE(buf, layout.offOrigin, 1.25)
	putFloat32LE(buf, layout.offOrigin+4, 2.5)
	putFloat32LE(buf, layout.offOrigin+8, 3.75)
	putFloat32LE(buf, layout.offVelocity, 4.25)
	putFloat32LE(buf, layout.offVelocity+4, 5.5)
	putFloat32LE(buf, layout.offVelocity+8, 6.75)
	putFloat32LE(buf, layout.offViewAngles, 7.25)
	putFloat32LE(buf, layout.offViewAngles+4, 8.5)
	putFloat32LE(buf, layout.offViewAngles+8, 9.75)

	s := parseSample(buf, &layout)
	if s.ClientNum != 7 || s.Health != 93 || s.Team != 2 || s.PmType != 1 || s.PmFlags != 0x4000 || s.Weaponstate != 5 {
		t.Fatalf("unexpected scalar fields: %+v", s)
	}
	if s.Weapon != 14 {
		t.Fatalf("unexpected weapon: got %d want 14", s.Weapon)
	}
	if s.Origin != [3]float32{1.25, 2.5, 3.75} {
		t.Fatalf("unexpected origin: %#v", s.Origin)
	}
	if s.Velocity != [3]float32{4.25, 5.5, 6.75} {
		t.Fatalf("unexpected velocity: %#v", s.Velocity)
	}
	if s.ViewAngles != [3]float32{7.25, 8.5, 9.75} {
		t.Fatalf("unexpected view angles: %#v", s.ViewAngles)
	}
}
