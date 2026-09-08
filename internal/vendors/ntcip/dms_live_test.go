package ntcip

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp"
)

// TestLiveDMSRead polls a real sign when DMS_SNMP_ADDR is set (host:port).
// Skipped in CI. Address stays in the environment, not in the repo.
func TestLiveDMSRead(t *testing.T) {
	addr := os.Getenv("DMS_SNMP_ADDR")
	if addr == "" {
		t.Skip("set DMS_SNMP_ADDR=host:161 to poll a real sign")
	}
	community := os.Getenv("DMS_SNMP_COMMUNITY")
	if community == "" {
		community = "public"
	}
	c, err := snmp.Dial(snmp.DialConfig{
		Address: addr, Community: community, Version: "v1",
		Timeout: 3 * time.Second, Retries: 1,
	})
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snap, err := NewDMS("dms-lab", c).Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if snap.FacetFailed(model.KindDMSStatus) {
		t.Fatalf("dms-status facet error: %+v", snap.Errors)
	}
	f, ok := snap.Facet(model.KindDMSStatus)
	if !ok {
		t.Fatalf("missing dms-status facet; errors=%+v", snap.Errors)
	}
	st := f.(model.DMSStatus)
	t.Logf("live DMSStatus: %+v", st)
	if st.ControlMode == model.ControlUnknown {
		t.Fatal("control mode unknown — OID answered but unmapped")
	}
}
