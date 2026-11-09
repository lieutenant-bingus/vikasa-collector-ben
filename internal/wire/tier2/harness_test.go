package tier2_test

import (
	"slices"
	"testing"

	conformance "github.com/Vikasa2M/openits-models/tools/conformance/tests"

	"github.com/Vikasa2M/vikasa-collector/internal/wire/openits"
)

// stateChecks are the harness checks that judge a device's YANG state tree
// (Observation.Device): its identity, configuration, timing, diagnostics.
// A collector publishes events and never that tree, so none of them can
// apply. The four that also read events (TestHealth_OperationalStatus and
// the three *MirrorsLiveState / *ZoneIsConfigured checks) compare events
// against that tree, and so are ruled out with it.
var stateChecks = []string{
	"TestCabinetPower_BatteryChargeReported",
	"TestCabinetPower_OnBatteryHasRuntime",
	"TestCabinetPower_SourceReported",
	"TestCctvControl_HeldImpliesHolder",
	"TestCctvIdentity_CameraID",
	"TestCctvPtz_ActivePresetIsDefined",
	"TestCctvStream_OkHealthHasBitrate",
	"TestCctvTour_RunningImpliesActiveTour",
	"TestCctv_OperationalStatusPresent",
	"TestCctv_VelocityMoveRequiresVelocityConfig",
	"TestChannels_AtLeastOne",
	"TestChannels_NoConflictingFlashYellow",
	"TestChannels_SourceResolves",
	"TestConflictMonitor_AtLeastOnePermissive",
	"TestConflictMonitor_NoSameRingPermissive",
	"TestConflictMonitor_PermissiveCanonicalOrder",
	"TestConflictMonitor_PermissiveResolvesChannels",
	"TestCoordination_ActivePlan",
	"TestCoordination_BarrierAssignment",
	"TestCoordination_BarrierCrossingAlignment",
	"TestCoordination_NEMADualRing",
	"TestCoordination_SplitsWithinCycle",
	"TestDMSCapabilities_CharMatrixHasCellSize",
	"TestDMSCapabilities_PitchMatchesGeometry",
	"TestDMSCapabilities_SignTypePresent",
	"TestDMSControl_IlluminationControlPresent",
	"TestDMSControl_PhotocellModeReportsAmbient",
	"TestDMSDiagnostics_LampsFailedBound",
	"TestDMSDiagnostics_PixelsFailedBound",
	"TestDMSDiagnostics_StuckPixelsWithinFailed",
	"TestDMSFallback_PowerLossActivePresent",
	"TestDMSFallback_ReferencesResolve",
	"TestDMSFallback_TriggerExplainsFallbackState",
	"TestDMSIdentity_Dimensions",
	"TestDMSIdentity_Firmware",
	"TestDMSIdentity_Location",
	"TestDMSIdentity_MakeModel",
	"TestDMSIdentity_SignID",
	"TestDMSMessages_ActiveBeaconMatchesSlot",
	"TestDMSMessages_ActiveMatchesSlot",
	"TestDMSMessages_BufferIntegrity",
	"TestDMSMessages_GraphicsReferencedExist",
	"TestDMSMessages_SlotStatusValid",
	"TestDMSMessages_StoredRowMatchesAuthored",
	"TestDMSOperational_BrightnessInRange",
	"TestDMSOperational_DisplayStatePresent",
	"TestDMSOperational_ModePresent",
	"TestDMSPower_OffGridReportsCharge",
	"TestDMSSchedule_DayPlanHasAction",
	"TestDetectors_AssignedToPhase",
	"TestDetectors_AtLeastOne",
	"TestDetectors_MeasurementReported",
	"TestHealth_NotFlashing",
	"TestHealth_OperationalStatus",
	"TestIdentity_ControllerID",
	"TestIdentity_Firmware",
	"TestIdentity_Location",
	"TestIdentity_MakeModel",
	"TestMonitor_StatusReported",
	"TestOperation_ActiveFlagsMatchLists",
	"TestOverlaps_AtLeastOne",
	"TestOverlaps_FYAProtectedAndOpposingPhasesResolve",
	"TestPerceptionIdentity_SensorID",
	"TestPerceptionIncident_ConfidencePresent",
	"TestPerceptionIncident_SeverityPresent",
	"TestPerceptionTrack_LifecyclePresent",
	"TestPerception_DispositionRoundTrip",
	"TestPhaseTiming_HasPhases",
	"TestPhaseTiming_MaxGreenSane",
	"TestPhaseTiming_MinGreenMinimum",
	"TestPhaseTiming_PedClearSane",
	"TestPhaseTiming_RedClearNonNegative",
	"TestPhaseTiming_YellowChangeMinimum",
	"TestPreemption_RailTrackClearance",
	"TestSequences_PhasesMatchRingMembership",
	"TestTimebase_ReferencesResolve",
	"TestTrafficSensorConfig_ClassificationSchemePresent",
	"TestTrafficSensorConfig_LaneNumberingOrigin",
	"TestTrafficSensorConfig_MountingSideForFromSensor",
	"TestTrafficSensorDiag_CalibrationStatusKnown",
	"TestTrafficSensorHealth_RollupConsistent",
	"TestTrafficSensorIdentity_SensorID",
	"TestTrafficSensorInterval_ClassVolumeReconciles",
	"TestTrafficSensorInterval_DataQualityPresent",
	"TestTrafficSensorInterval_SpaceMeanNotAboveTimeMean",
	"TestTrafficSensorInterval_UptimeInRange",
	"TestTrafficSensorLane_OperationalStatusPresent",
	"TestTrafficSensorLane_ZoneLengthPositive",
	"TestTrafficSensorQueue_LengthWhenQueueing",
	"TestYANG_Validate",
	"TestZoneOccupancyEvent_ChangedMirrorsLiveState",
	"TestZoneOccupancyEvent_ChangedZoneIsConfigured",
	"TestZoneOccupancy_ClassifiesGatesBreakdown",
	"TestZoneOccupancy_LiveZonesAreConfigured",
	"TestZoneOccupancy_ObservedAtPresent",
	"TestZoneOccupancy_OccupiedSinceOnlyWhenPresent",
	"TestZoneOccupancy_PresentClassSumsToCount",
}

// unpublished are harness checks that require an event the collector never
// publishes, keyed to that ce-type. Both CCTV ones describe the outcome of a
// command (allowlisted as describesACommand in the emitter's catalog test),
// and the collector only reads. Mapping the ce-type makes the guard below
// fail, so the check starts running when the event does.
var unpublished = map[string]string{
	"TestCctvEvent_PresetRecalledShape": "openits.cctv.ptz-preset-recalled.v1",
	"TestCctvEvent_LockoutDeniedShape":  "openits.cctv.lockout-denied.v1",
}

// A release that adds a harness check must not leave it silently unrun: for
// every harness kind this test judges, each check the pinned harness defines
// is either run (checks) or ruled out as judging device state (stateChecks).
// A new check fails here until someone decides which it is. The reverse
// holds too: a name in either list the harness no longer defines is stale.
func TestEveryHarnessCheckIsRunOrRuledOut(t *testing.T) {
	defined := map[string]bool{}
	for kind, run := range checks {
		for _, tc := range conformance.All(kind) {
			defined[tc.Name] = true
			_, notPublished := unpublished[tc.Name]
			ran, ruledOut := slices.Contains(run, tc.Name), slices.Contains(stateChecks, tc.Name) || notPublished
			switch {
			case ran && ruledOut:
				t.Errorf("%s: %s is both run and ruled out", kind, tc.Name)
			case !ran && !ruledOut:
				t.Errorf("%s: the harness defines %s, which this test neither runs nor rules out; "+
					"add it to checks if it judges events we publish, to stateChecks if it judges the device's state tree, "+
					"or to unpublished if it needs an event the collector does not emit", kind, tc.Name)
			}
		}
	}
	for _, name := range stateChecks {
		if !defined[name] {
			t.Errorf("stateChecks names %s, which no harness kind this test judges defines any more", name)
		}
	}
	mapped := openits.New("tier2", "epoch").CETypes()
	for name, ceType := range unpublished {
		if !defined[name] {
			t.Errorf("unpublished names %s, which no harness kind this test judges defines any more", name)
		}
		if slices.Contains(mapped, ceType) {
			t.Errorf("%s is mapped now, so %s can run: move it from unpublished to checks", ceType, name)
		}
	}
}
