package replay

import "testing"

func TestDelayLive(t *testing.T) {
	r := &Replay{}
	for ms := int32(0); ms <= 200_000; ms += 1000 {
		r.Frames = append(r.Frames, Frame{ServerTime: ms})
		r.Events = append(r.Events, Event{ServerTime: ms})
	}
	// newest frame 200 s; a 90 s delay shows up to 110 s
	d := DelayLive(r, 90_000)
	if got := d.Frames[len(d.Frames)-1].ServerTime; got != 110_000 {
		t.Fatalf("last delayed frame at %d, want 110000", got)
	}
	if got := d.Events[len(d.Events)-1].ServerTime; got != 110_000 {
		t.Fatalf("last delayed event at %d, want 110000", got)
	}
	// anything shorter than the minimum is raised to it
	d = DelayLive(r, 10_000)
	if got := d.Frames[len(d.Frames)-1].ServerTime; got != 110_000 {
		t.Fatalf("a 10 s delay showed up to %d, want 110000", got)
	}
	// a longer delay is honoured
	d = DelayLive(r, 120_000)
	if got := d.Frames[len(d.Frames)-1].ServerTime; got != 80_000 {
		t.Fatalf("120 s delay: last frame %d, want 80000", got)
	}
	// less than the delay recorded: nothing to show yet
	short := &Replay{Frames: []Frame{{ServerTime: 0}, {ServerTime: 50_000}}}
	if d := DelayLive(short, 90_000); len(d.Frames) != 0 {
		t.Fatalf("%d frames shown of a 50 s recording, want none", len(d.Frames))
	}
	if d := DelayLive(&Replay{}, 90_000); len(d.Frames) != 0 {
		t.Fatal("frames from nothing")
	}
}
