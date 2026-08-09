package model

// KindCCTVStatus is what a camera reports about how it is being driven and
// what its tours are doing.
//
// Scope note: this is camera MANAGEMENT state, not video analytics. A camera
// running detection also produces perception facets, under a separate device
// kind — entity kind follows the event family, not the chassis, so one
// physical camera can legitimately appear as both.
const KindCCTVStatus Kind = "cctv-status"

// CCTVControlMode is who is driving the camera.
//
// The upstream set is central / local / central-override / other, with no
// "unknown" member — so like DMSControlMode, an unknown control mode is not
// expressible on the wire and the emitter declines rather than guessing.
type CCTVControlMode uint8

const (
	CCTVControlUnknown CCTVControlMode = iota
	CCTVControlCentral
	CCTVControlLocal
	CCTVControlCentralOverride
	CCTVControlOther
)

func (m CCTVControlMode) String() string {
	switch m {
	case CCTVControlCentral:
		return "central"
	case CCTVControlLocal:
		return "local"
	case CCTVControlCentralOverride:
		return "central-override"
	case CCTVControlOther:
		return "other"
	default:
		return "unknown"
	}
}

// TourRunState is whether a preset tour is running.
type TourRunState uint8

const (
	TourUnknown TourRunState = iota
	TourStopped
	TourRunning
	TourPaused
)

func (s TourRunState) String() string {
	switch s {
	case TourStopped:
		return "stopped"
	case TourRunning:
		return "running"
	case TourPaused:
		return "paused"
	default:
		return "unknown"
	}
}

// CCTVTour is one configured preset tour and its current run state.
type CCTVTour struct {
	TourID uint32
	State  TourRunState
}

// CCTVStatus is a camera's management state at one poll. A camera with no
// tours configured yields an empty Tours slice, not an error.
type CCTVStatus struct {
	ControlMode CCTVControlMode
	Tours       []CCTVTour // sorted by TourID
}

func (CCTVStatus) FacetKind() Kind { return KindCCTVStatus }
