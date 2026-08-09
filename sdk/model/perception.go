package model

// KindZoneIncidents is the set of incidents a perception sensor currently
// observes in its detection zones.
const KindZoneIncidents Kind = "zone-incidents"

// IncidentType is what the sensor thinks is happening. Closed set: these are
// the conditions the model defines, and a sensor reporting something else is
// reporting IncidentUnknown rather than inventing a category.
type IncidentType uint8

const (
	IncidentUnknown IncidentType = iota
	IncidentStoppedVehicle
	IncidentWrongWayVehicle
	IncidentPedestrianInRoadway
	IncidentDebrisInRoadway
	IncidentQueueSpillback
	IncidentNearMiss
	IncidentCongestion
	IncidentSlowedTraffic
)

func (t IncidentType) String() string {
	switch t {
	case IncidentStoppedVehicle:
		return "stopped-vehicle"
	case IncidentWrongWayVehicle:
		return "wrong-way-vehicle"
	case IncidentPedestrianInRoadway:
		return "pedestrian-in-roadway"
	case IncidentDebrisInRoadway:
		return "debris-in-roadway"
	case IncidentQueueSpillback:
		return "queue-spillback"
	case IncidentNearMiss:
		return "near-miss"
	case IncidentCongestion:
		return "congestion"
	case IncidentSlowedTraffic:
		return "slowed-traffic"
	default:
		return "unknown"
	}
}

// ObjectClass is what the sensor classified the object as.
type ObjectClass uint8

const (
	ObjectUnknown ObjectClass = iota
	ObjectPassengerVehicle
	ObjectTruck
	ObjectBus
	ObjectMotorcycle
	ObjectBicycle
	ObjectPedestrian
	ObjectAnimal
	ObjectDebris
)

func (c ObjectClass) String() string {
	switch c {
	case ObjectPassengerVehicle:
		return "passenger-vehicle"
	case ObjectTruck:
		return "truck"
	case ObjectBus:
		return "bus"
	case ObjectMotorcycle:
		return "motorcycle"
	case ObjectBicycle:
		return "bicycle"
	case ObjectPedestrian:
		return "pedestrian"
	case ObjectAnimal:
		return "animal"
	case ObjectDebris:
		return "debris"
	default:
		return "unknown"
	}
}

// IncidentSeverity is how serious the sensor rates an incident.
//
// SeverityUnreported is the zero value and means the sensor did not rate it.
// That is NOT the same as minor, and the distinction has to survive into the
// emitter: see the mapping, where the wire cannot express it.
type IncidentSeverity uint8

const (
	SeverityUnreported IncidentSeverity = iota
	IncidentMinor
	IncidentIntermediate
	IncidentMajor
)

func (s IncidentSeverity) String() string {
	switch s {
	case IncidentMinor:
		return "minor"
	case IncidentIntermediate:
		return "intermediate"
	case IncidentMajor:
		return "major"
	default:
		return "unreported"
	}
}

// ZoneIncident is one incident currently observed in a detection zone.
//
// IncidentID is the sensor's own stable identifier for the condition and is
// what the differ keys on — the same role Fault.ID plays. It must survive
// across polls for as long as the incident does, or every poll would report
// the incident as cleared and re-detected.
//
// TrackID identifies the object within the sensor's tracker. TrackEpoch exists
// because trackers reuse ids after a restart: the pair is what actually
// identifies an object, and TrackID alone silently conflates two vehicles
// across a reboot.
//
// SpeedReported distinguishes an unreported speed from a genuine zero — and a
// stopped-vehicle incident is precisely where zero is the meaningful value.
type ZoneIncident struct {
	IncidentID         string
	ZoneID             string
	Type               IncidentType
	Severity           IncidentSeverity
	ObjectClass        ObjectClass
	SpeedHundredthsKPH uint32
	SpeedReported      bool
	ConfidencePercent  uint8
	TrackID            uint32
	TrackEpoch         uint32
}

// ZoneIncidents is every incident active at one poll. A sensor observing a
// quiet road yields an EMPTY facet, not an error.
type ZoneIncidents struct{ Incidents []ZoneIncident }

func (ZoneIncidents) FacetKind() Kind { return KindZoneIncidents }
