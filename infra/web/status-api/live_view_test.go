package main

import (
	"testing"
	"time"
)

func TestLiveViewDelayNeverBelowMinimum(t *testing.T) {
	for env, want := range map[string]time.Duration{
		"":    90 * time.Second,
		"abc": 90 * time.Second,
		"0":   90 * time.Second,
		"30":  90 * time.Second,
		"89":  90 * time.Second,
		"-5":  90 * time.Second,
		"90":  90 * time.Second,
		"120": 120 * time.Second,
	} {
		if got := liveViewDelay(env); got != want {
			t.Errorf("LIVE_VIEW_DELAY_SECONDS=%q gave %v, want %v", env, got, want)
		}
	}
}
