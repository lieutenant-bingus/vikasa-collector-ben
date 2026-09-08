package maxtime

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

func TestASCLive(t *testing.T) {
	baseURL := os.Getenv("MAXTIME_BASE_URL")
	if baseURL == "" {
		t.Skip("set MAXTIME_BASE_URL to run against a controller")
	}
	username, password := os.Getenv("MAXTIME_USERNAME"), os.Getenv("MAXTIME_PASSWORD")
	var credentials []string
	if username != "" || password != "" {
		if username == "" || password == "" {
			t.Fatal("MAXTIME_USERNAME and MAXTIME_PASSWORD must be provided together")
		}
		credentials = []string{username, password}
	}
	client, err := newHTTPMIBReader(baseURL, 5*time.Second, credentials...)
	if err != nil {
		t.Fatalf("newHTTPMIBReader: %v", err)
	}
	snap, err := NewASC("maxtime-live", client).Read(context.Background())
	if err != nil {
		t.Fatalf("live Read: %v", err)
	}
	signal, ok := snap.Facet(model.KindSignalStatus)
	if !ok {
		t.Fatal("live snapshot missing signal-status facet")
	}
	if got := signal.(model.SignalStatus); got.Mode != model.ModeNormal {
		t.Fatalf("live controller mode = %v, want normal for controllerOpMode=5", got.Mode)
	}
	if got := signal.(model.SignalStatus); got.ActivePlanID != 0 {
		t.Fatalf("live active plan = %d, want 0 for PatrnSta=254 (Free)", got.ActivePlanID)
	}
	if signal.(model.SignalStatus).InConflictFlash {
		t.Fatal("live controller reports flash active despite FlashSta=2 (Off)")
	}
	t.Logf("live snapshot: facets=%d errors=%+v", len(snap.Facets), snap.Errors)
}
