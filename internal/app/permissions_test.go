package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/internal/durable"
	"github.com/Vikasa2M/vikasa-collector/internal/publish"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

// leastPrivilege is the broker grant the deploy guide documents (ADR 0022
// decision 3), for tenant us-ga/metro/d01 under the default subject
// template. It is minimal as well as sufficient: removing any one entry
// makes TestLeastPrivilegeGrantIsSufficient fail (checked entry by entry
// when the set was derived). The subscribe side must name _INBOX.> rather
// than be left empty: an empty allow list is NATS for "allow everything".
func leastPrivilege() jwt.Permissions {
	const kv = "KV_" + durable.Bucket
	var pub []string
	pub = append(pub,
		"openits.us-ga.metro.d01.>",
		"openits-collector.us-ga.metro.d01.>",
		"$KV."+durable.Bucket+".>",
		"$JS.API.INFO",
	)
	for _, stream := range []string{"OPENITS-US-GA-METRO-D01", "OPENITS-COLLECTOR-US-GA-METRO-D01", kv} {
		pub = append(pub,
			"$JS.API.STREAM.CREATE."+stream,
			"$JS.API.STREAM.UPDATE."+stream,
		)
	}
	pub = append(pub,
		"$JS.API.CONSUMER.CREATE."+kv+".>",
		"$JS.API.CONSUMER.DELETE."+kv+".>",
		"$JS.API.DIRECT.GET."+kv+".>",
	)
	var p jwt.Permissions
	p.Pub.Allow.Add(pub...)
	p.Sub.Allow.Add("_INBOX.>")
	return p
}

// violations collects the server's permission-violation log lines.
type violations struct {
	mu    sync.Mutex
	lines []string
}

func (v *violations) record(format string, args ...any) {
	if line := fmt.Sprintf(format, args...); strings.Contains(line, "Violation") {
		v.mu.Lock()
		v.lines = append(v.lines, line)
		v.mu.Unlock()
	}
}
func (v *violations) Noticef(f string, a ...any) { v.record(f, a...) }
func (v *violations) Warnf(f string, a ...any)   { v.record(f, a...) }
func (v *violations) Fatalf(f string, a ...any)  { v.record(f, a...) }
func (v *violations) Errorf(f string, a ...any)  { v.record(f, a...) }
func (v *violations) Debugf(f string, a ...any)  { v.record(f, a...) }
func (v *violations) Tracef(f string, a ...any)  { v.record(f, a...) }

func (v *violations) all() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.lines...)
}

// jwtBroker runs an operator-mode server and returns its URL and .creds
// files for two users of one JetStream account: one holding perms, and an
// unrestricted observer for the test's own reads.
func jwtBroker(t *testing.T, perms jwt.Permissions) (ns *server.Server, collectorCreds, observerCreds string, v *violations) {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	opKP, err := nkeys.CreateOperator()
	must(err)
	opPub, err := opKP.PublicKey()
	must(err)
	oc := jwt.NewOperatorClaims(opPub)
	opJWT, err := oc.Encode(opKP)
	must(err)
	opClaims, err := jwt.DecodeOperatorClaims(opJWT)
	must(err)

	account := func(js bool) (nkeys.KeyPair, string, string) {
		kp, err := nkeys.CreateAccount()
		must(err)
		pub, err := kp.PublicKey()
		must(err)
		ac := jwt.NewAccountClaims(pub)
		if js {
			ac.Limits.JetStreamLimits = jwt.JetStreamLimits{DiskStorage: -1, MemoryStorage: -1, Streams: -1, Consumer: -1}
		}
		tok, err := ac.Encode(opKP)
		must(err)
		return kp, pub, tok
	}
	_, sysPub, sysJWT := account(false)
	accKP, accPub, accJWT := account(true)

	resolver := &server.MemAccResolver{}
	must(resolver.Store(sysPub, sysJWT))
	must(resolver.Store(accPub, accJWT))

	ns, err = server.NewServer(&server.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(),
		TrustedOperators: []*jwt.OperatorClaims{opClaims},
		AccountResolver:  resolver,
		SystemAccount:    sysPub,
	})
	must(err)
	v = &violations{}
	ns.SetLogger(v, false, false)
	ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}

	creds := func(name string, p *jwt.Permissions) string {
		kp, err := nkeys.CreateUser()
		must(err)
		pub, err := kp.PublicKey()
		must(err)
		uc := jwt.NewUserClaims(pub)
		uc.IssuerAccount = accPub
		if p != nil {
			uc.Permissions = *p
		}
		tok, err := uc.Encode(accKP)
		must(err)
		seed, err := kp.Seed()
		must(err)
		file, err := jwt.FormatUserConfig(tok, seed)
		must(err)
		path := filepath.Join(t.TempDir(), name+".creds")
		must(os.WriteFile(path, file, 0o600))
		return path
	}
	return ns, creds("collector", &perms), creds("observer", nil), v
}

// The documented grant is enough for everything the collector does — both
// streams provisioned and written, the synth-state bucket created, loaded
// and written — with not one permission violation (ADR 0022 decision 3).
func TestLeastPrivilegeGrantIsSufficient(t *testing.T) {
	ns, collectorCreds, observerCreds, v := jwtBroker(t, leastPrivilege())

	reg := adapter.NewRegistry()
	reg.Register(adapter.Descriptor{Vendor: "fixture", DeviceKind: "asc", Caps: adapter.CapState},
		func(string, map[string]any) (adapter.Adapter, error) { return &flakyASC{}, nil })
	cfg := &config.Config{
		Region: "us-ga", Agency: "metro", AgencyUnit: "d01", Site: "cab-1",
		CollectorID: "metro-cab-1-collector", ModelVersion: "openits/v1",
		HeartbeatInterval: 50 * time.Millisecond,
		Devices: []config.Device{
			{ID: "asc-1", Vendor: "fixture", DeviceKind: "asc", PollInterval: 20 * time.Millisecond},
		},
	}

	nc, err := nats.Connect(ns.ClientURL(), nats.UserCredentials(observerCreds))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}

	// boot runs the collector until cond holds, then stops it cleanly.
	boot := func(what string, cond func(logged string) bool) {
		t.Helper()
		var buf lockedLog
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			done <- Run(ctx, cfg, reg, Options{
				NATSURL: ns.ClientURL(), NATS: publish.Security{CredsFile: collectorCreds},
				Version: "test", Logger: slog.New(slog.NewTextHandler(&buf, nil)),
			})
		}()
		deadline := time.Now().Add(8 * time.Second)
		for !cond(buf.String()) {
			if time.Now().After(deadline) {
				cancel()
				t.Fatalf("%s: not reached under the grant; violations: %q", what, v.all())
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: Run did not return after cancel", what)
		}
	}

	// First boot: both streams provisioned and written, the bucket created
	// and written.
	boot("first boot", func(string) bool {
		for _, stream := range []string{"OPENITS-US-GA-METRO-D01", "OPENITS-COLLECTOR-US-GA-METRO-D01"} {
			s, err := js.Stream(t.Context(), stream)
			if err != nil || s.CachedInfo().State.Msgs == 0 {
				return false
			}
		}
		kv, err := js.KeyValue(t.Context(), durable.Bucket)
		if err != nil {
			return false
		}
		st, err := kv.Status(t.Context())
		return err == nil && st.Values() > 0
	})
	// Second boot: the stored state is read back, the path a first boot on
	// an empty bucket never takes.
	boot("second boot", func(logged string) bool {
		return strings.Contains(logged, "synth state loaded")
	})

	if got := v.all(); len(got) > 0 {
		t.Fatalf("the documented grant is not enough; the broker refused:\n%s", strings.Join(got, "\n"))
	}
}

type lockedLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
