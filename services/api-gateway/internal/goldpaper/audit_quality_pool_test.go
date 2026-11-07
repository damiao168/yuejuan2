package goldpaper

import "testing"

func TestImagePoolIsStableAcrossNominations(t *testing.T) {
	store := NewMemoryStore()
	calibration := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	blind := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if store.registerImagePoolLocked("tenant", calibration, "") != PoolCalibration || store.registerImagePoolLocked("tenant", blind, "") != PoolBlind {
		t.Fatal("content partition failed")
	}
	if store.registerImagePoolLocked("tenant", calibration, PoolBlind) != PoolCalibration {
		t.Fatal("exposed image moved to blind pool")
	}
	if store.registerImagePoolLocked("tenant", blind, PoolCalibration) != PoolBlind {
		t.Fatal("blind image moved to calibration pool")
	}
}
