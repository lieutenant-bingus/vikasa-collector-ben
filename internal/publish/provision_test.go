package publish

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// bootOnce runs a publisher until one event is published — which means the
// stream has been provisioned — and then drains it: one collector boot.
func bootOnce(t *testing.T, url string) {
	t.Helper()
	p, err := start(t.Context(), url, testTemplate(t), []string{healthType}, quiet(), fast(100))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Enqueue(envN(0, nil), "cab-1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a publish", func() bool { return p.Stats().Published == 1 })
	drainBounded(p)
}

// operatorSets applies an operator's change to the collector's stream,
// starting from its current config as an operator would.
func operatorSets(t *testing.T, js jetstream.JetStream, change func(*jetstream.StreamConfig)) {
	t.Helper()
	st, err := js.Stream(t.Context(), healthStream)
	if err != nil {
		t.Fatal(err)
	}
	cfg := st.CachedInfo().Config
	change(&cfg)
	if _, err := js.UpdateStream(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
}

func streamConfig(t *testing.T, js jetstream.JetStream) jetstream.StreamConfig {
	t.Helper()
	st, err := js.Stream(t.Context(), healthStream)
	if err != nil {
		t.Fatal(err)
	}
	return st.CachedInfo().Config
}

var healthStream = StreamNameForBinding("openits-collector.us-ga.metro.d01.>")

// Retention belongs to the broker's operator (consumer contract). Before,
// every boot replaced the whole stream config, so limits an operator set
// were silently reset to unlimited — on cabinet flash, a disk-filling bug.
func TestProvisioningKeepsOperatorRetention(t *testing.T) {
	ns := startNATS(t)
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)

	bootOnce(t, ns.ClientURL())
	operatorSets(t, js, func(c *jetstream.StreamConfig) {
		c.MaxAge = time.Hour
		c.MaxBytes = 64 << 20
		c.MaxMsgs = 100_000
	})
	bootOnce(t, ns.ClientURL())

	got := streamConfig(t, js)
	if got.MaxAge != time.Hour || got.MaxBytes != 64<<20 || got.MaxMsgs != 100_000 {
		t.Fatalf("after a restart: MaxAge=%v MaxBytes=%d MaxMsgs=%d; the operator's limits were reset", got.MaxAge, got.MaxBytes, got.MaxMsgs)
	}
}

// What the collector depends on, it restores — keeping everything else. A
// duplicate window shorter than the collector's breaks ce-id dedup across a
// reconnect, so it is raised; a longer one an operator chose is kept.
func TestProvisioningRestoresOnlyWhatTheCollectorNeeds(t *testing.T) {
	ns := startNATS(t)
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)

	bootOnce(t, ns.ClientURL())
	operatorSets(t, js, func(c *jetstream.StreamConfig) {
		c.Duplicates = 10 * time.Second
		c.MaxAge = time.Hour
	})
	bootOnce(t, ns.ClientURL())
	if got := streamConfig(t, js); got.Duplicates != dedupWindow || got.MaxAge != time.Hour {
		t.Fatalf("short window: Duplicates=%v MaxAge=%v; want the window raised to %v and MaxAge kept", got.Duplicates, got.MaxAge, dedupWindow)
	}

	operatorSets(t, js, func(c *jetstream.StreamConfig) { c.Duplicates = 10 * time.Minute })
	bootOnce(t, ns.ClientURL())
	if got := streamConfig(t, js); got.Duplicates != 10*time.Minute {
		t.Fatalf("long window: Duplicates=%v; an operator's longer window must be kept", got.Duplicates)
	}
}
