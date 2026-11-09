package ntcip

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp/snmptest"
)

// The fuzz targets below feed each adapter whatever a device could send:
// any value, or no answer, for every OID its Read asks for. SECURITY.md puts
// "a malformed or malicious device response that crashes the collector" in
// scope; the runner recovers an adapter panic, but a decode that panics is
// still a device that can never be read.

// ascOIDs is every OID ntcip-asc can ask for: its scalars and the first
// eight detector channels.
func ascOIDs() []string {
	oids := []string{
		oidOperationStatus, oidFlashStatus, oidPatternStatus, oidPreemptStatus,
		oidShortAlarmStatus, oidMaxVehicleDetectors,
	}
	for ch := 1; ch <= 8; ch++ {
		oids = append(oids,
			fmt.Sprintf("%s.%d", oidDetectorVolumeCol, ch),
			fmt.Sprintf("%s.%d", oidDetectorOccupancyCol, ch))
	}
	return oids
}

var dmsIntOIDs = []string{
	oidDMSControlMode, oidDMSMsgSourceMode, oidDMSActivateMsgErr, oidDMSMultiSyntaxErr,
	oidDMSMultiSyntaxPos, oidDMSIllumMaxPhotocell, oidDMSIllumPhotocell, oidDMSIllumNumBright,
	oidDMSIllumBrightLevel, oidDMSStatDoorOpen, oidDMSTempFace, oidDMSTempCabinet,
	oidDMSHumidityNumRows, oidDMSHumidity1,
}

// intsFrom spends nine bytes per OID: a presence byte (odd means answered)
// and a big-endian int64. Short input leaves the rest unanswered.
func intsFrom(data []byte, oids []string) map[string]int64 {
	out := map[string]int64{}
	for _, oid := range oids {
		if len(data) < 9 {
			break
		}
		if data[0]%2 == 1 {
			out[oid] = int64(binary.BigEndian.Uint64(data[1:9])) //nolint:gosec // any bit pattern is a legal fuzz value
		}
		data = data[9:]
	}
	return out
}

// bytesFor encodes a fixture back into intsFrom's form, for seeds.
func bytesFor(vals map[string]int64, oids []string) []byte {
	var out []byte
	for _, oid := range oids {
		v, ok := vals[oid]
		var rec [9]byte
		if ok {
			rec[0] = 1
			binary.BigEndian.PutUint64(rec[1:], uint64(v)) //nolint:gosec // round-trips intsFrom
		}
		out = append(out, rec[:]...)
	}
	return out
}

// checkSnapshot holds what any Read must satisfy whatever it was sent.
func checkSnapshot(t *testing.T, snap *model.Snapshot, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Read against a fake that never fails returned %v", err)
	}
	for _, f := range snap.Facets {
		if f == nil {
			t.Fatal("nil facet")
		}
		if snap.FacetFailed(f.FacetKind()) {
			t.Fatalf("%s is both present and failed", f.FacetKind())
		}
		switch v := f.(type) {
		case model.DetectorSamples:
			for _, s := range v.Samples {
				if s.OccupancyTenths > 1000 {
					t.Fatalf("occupancy %d tenths exceeds 100%%", s.OccupancyTenths)
				}
			}
		case model.DMSEnvironment:
			if v.BrightnessPercent > 100 || v.AmbientLightPercent > 100 {
				t.Fatalf("percent out of range: brightness %d, ambient %d", v.BrightnessPercent, v.AmbientLightPercent)
			}
		}
	}
}

func FuzzASCRead(f *testing.F) {
	oids := ascOIDs()
	f.Add(bytesFor(healthyFixture, oids))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		snap, err := NewASC("asc-1", &snmptest.Static{Values: intsFrom(data, oids)}).Read(t.Context())
		checkSnapshot(t, snap, err)
	})
}

func FuzzDMSRead(f *testing.F) {
	f.Add(bytesFor(healthyDMSInts, dmsIntOIDs),
		healthyDMSOctets[oidDMSMsgTableSource], healthyDMSOctets[oidDMSCurrentBufferMULTI], true, true)
	f.Add([]byte{}, []byte{}, []byte{}, false, false)
	f.Fuzz(func(t *testing.T, ints, msgID, multi []byte, hasMsgID, hasMulti bool) {
		octets := map[string][]byte{}
		if hasMsgID {
			octets[oidDMSMsgTableSource] = msgID
		}
		if hasMulti {
			octets[oidDMSCurrentBufferMULTI] = multi
		}
		snap, err := NewDMS("dms-1", &snmptest.Static{Values: intsFrom(ints, dmsIntOIDs), Octets: octets}).Read(t.Context())
		checkSnapshot(t, snap, err)
	})
}
