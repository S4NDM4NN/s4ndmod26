package demostore

import (
	"context"
	"errors"
	"testing"
)

func addMatch(t *testing.T, s *Store, c byte, owner string) string {
	t.Helper()
	d, _, err := s.AddDemo(context.Background(), demo(c, "P"+string(c), 1000, 9000), owner, "x.dm_60", Attachment{})
	if err != nil {
		t.Fatal(err)
	}
	return d.MatchID
}

func TestSeriesLinkMatchesInOrder(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	m1, m2, m3 := addMatch(t, s, 'a', "travis"), addMatch(t, s, 'b', "travis"), addMatch(t, s, 'c', "travis")
	id, err := s.CreateSeries(ctx, "travis", "QCon03 final", []string{m2, m3, m1})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := s.SeriesOf(ctx, m3)
	if err != nil || ref == nil || ref.Pos != 2 || ref.Total != 3 || ref.Prev != m2 || ref.Next != m1 || ref.Name != "QCon03 final" {
		t.Fatalf("series of the middle match: %+v, %v", ref, err)
	}
	sr, err := s.GetSeries(ctx, id)
	if err != nil || len(sr.Matches) != 3 || sr.Matches[0].ID != m2 || sr.Matches[2].ID != m1 || sr.Matches[0].SeriesName != "QCon03 final" {
		t.Fatalf("GetSeries: %+v, %v", sr, err)
	}
	// reorder and drop one
	if err := s.UpdateSeries(ctx, "travis", id, "Final", []string{m1, m2}); err != nil {
		t.Fatal(err)
	}
	if ref, _ := s.SeriesOf(ctx, m3); ref != nil {
		t.Errorf("a match taken out is still in the series: %+v", ref)
	}
	if ref, _ := s.SeriesOf(ctx, m1); ref == nil || ref.Pos != 1 || ref.Name != "Final" {
		t.Errorf("after the reorder: %+v", ref)
	}
}

func TestSeriesNeedUploadsAndOwnership(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	mine, theirs := addMatch(t, s, 'a', "travis"), addMatch(t, s, 'b', "sam")
	if _, err := s.CreateSeries(ctx, "travis", "x", []string{mine, theirs}); !errors.Is(err, ErrNotYours) {
		t.Fatalf("linking a match the key has no demo of: %v", err)
	}
	if ref, _ := s.SeriesOf(ctx, mine); ref != nil {
		t.Fatal("a refused series left a link behind")
	}
	if _, err := s.CreateSeries(ctx, "travis", "x", []string{mine, "0123456789abcdef"}); !errors.Is(err, ErrNoSuchMatch) {
		t.Fatalf("linking a match that does not exist: %v", err)
	}
	id, err := s.CreateSeries(ctx, "travis", "mine", []string{mine})
	if err != nil {
		t.Fatal(err)
	}
	// sam uploaded the same file as travis: he has a demo in the match, so he may change the series
	d, _ := s.DemoBySHA(ctx, sha('a'))
	if _, err := s.AddUpload(ctx, d.ID, "sam", "a.dm_60"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSeries(ctx, "eve", id, "eve's", []string{mine}); !errors.Is(err, ErrNotYours) {
		t.Errorf("a key with no demo in the series changed it: %v", err)
	}
	if err := s.DeleteSeries(ctx, "eve", id); !errors.Is(err, ErrNotYours) {
		t.Errorf("a key with no demo in the series dissolved it: %v", err)
	}
	if err := s.UpdateSeries(ctx, "sam", id, "sam's", []string{mine}); err != nil {
		t.Errorf("sam, with a demo in the series, could not change it: %v", err)
	}
	// ... but adding a match takes a demo of that match, and a match in a series he cannot edit is not his to move
	other, err := s.CreateSeries(ctx, "sam", "sam's own", []string{theirs})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSeries(ctx, "eve", "stolen", []string{theirs}); !errors.Is(err, ErrNotYours) {
		t.Errorf("a key with no demo of the match linked it: %v", err)
	}
	if err := s.UpdateSeries(ctx, "sam", id, "sam's", []string{mine, theirs}); err != nil {
		t.Errorf("sam moving his own match between series he can edit: %v", err)
	}
	_ = other
	if err := s.DeleteSeries(ctx, "travis", id); err != nil {
		t.Fatal(err)
	}
	if ref, _ := s.SeriesOf(ctx, mine); ref != nil {
		t.Error("a dissolved series still holds its match")
	}
	if err := s.DeleteSeries(ctx, "travis", id); !errors.Is(err, ErrSeriesNotFound) {
		t.Errorf("dissolving twice: %v", err)
	}
}

func TestSeriesGoesWhenItsMatchesDo(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	m := addMatch(t, s, 'a', "travis")
	id, err := s.CreateSeries(ctx, "travis", "one", []string{m})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := s.DemoBySHA(ctx, sha('a'))
	if _, ok, err := s.RemoveUpload(ctx, d.ID, "travis"); err != nil || !ok {
		t.Fatalf("remove: %v %v", ok, err)
	}
	if sr, _ := s.GetSeries(ctx, id); sr != nil {
		t.Errorf("a series with no matches is still there: %+v", sr)
	}
}

func TestMergedMatchKeepsTheSeries(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	m1, m2 := addMatch(t, s, 'a', "travis"), addMatch(t, s, 'b', "travis")
	id, err := s.CreateSeries(ctx, "travis", "s", []string{m2})
	if err != nil {
		t.Fatal(err)
	}
	// a third demo bridges the two matches into m1; m2 was in the series
	if _, _, err := s.AddDemo(ctx, demo('c', "Pc", 1000, 9000), "travis", "c.dm_60", Attachment{MatchID: m1, Merge: map[string]int{m2: 0}}); err != nil {
		t.Fatal(err)
	}
	ref, err := s.SeriesOf(ctx, m1)
	if err != nil || ref == nil || ref.ID != id {
		t.Fatalf("the surviving match lost the series: %+v, %v", ref, err)
	}
}
