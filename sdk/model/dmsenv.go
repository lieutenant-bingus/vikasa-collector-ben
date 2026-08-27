package model

// KindDMSEnvironment is a sign's environment / illumination sensor cluster
// at one poll.
const KindDMSEnvironment Kind = "dms-environment"

// DMSEnvironment is the sensor cluster co-located with a sign: applied
// brightness and ambient light (the NTCIP dmsIllum group), housing
// temperatures, humidity, and the door/fan/heater booleans. It is a
// separate facet from DMSStatus because it is a separate read path with
// its own failure mode — a sign whose environment objects stop answering
// must not fail the display-status read, and vice versa.
//
// Every sensor is optional: signs ship with any subset of this cluster.
// A value's *Reported companion says whether the device actually answered
// for it this poll; when false the value field is meaningless and
// downstream encodes the reading as absent, never as zero. A missing
// sensor is Reported == false every poll — that is the sensor's normal
// state, not an error, so it never becomes a FacetError. The facet's zero
// value therefore honestly describes a sign with no environment sensors
// at all.
//
// Temperatures are tenths of a degree Celsius (DeciC), signed — the same
// lossless-at-device-precision reasoning as DetectorReading's
// OccupancyTenths. NTCIP signs report whole degrees; an adapter stores
// value*10. Rounding or unit conversion for the wire belongs to the
// emitter, never here.
type DMSEnvironment struct {
	// BrightnessPercent is the currently applied face brightness, 0-100.
	// It may sit below the operator's setpoint under auto-dim.
	BrightnessPercent  uint8
	BrightnessReported bool

	// AmbientLightPercent is the normalized photocell level, 0-100: the
	// device's own reported level scaled against its own reported maximum
	// (dmsIllumPhotocellLevelStatus / dmsIllumMaxPhotocellLevel). The
	// adapter performs that normalization — raw photocell levels are not
	// comparable across makes. NOT an illuminance measurement.
	AmbientLightPercent  uint8
	AmbientLightReported bool

	// IlluminanceLux is a calibrated photometric reading in lux. Only ever
	// populated from a real photometric sensor; never converted from the
	// normalized photocell level — no valid cross-device conversion exists.
	IlluminanceLux      uint32
	IlluminanceReported bool

	CabinetTempDeciC    int16 // cabinet interior temperature, tenths of °C
	CabinetTempReported bool

	FaceTempDeciC    int16 // display-face temperature, tenths of °C
	FaceTempReported bool

	HumidityPercent  uint8 // cabinet interior relative humidity, 0-100
	HumidityReported bool

	DoorOpen     bool // maintenance-door intrusion sensor
	DoorReported bool

	FanActive   bool // cabinet/enclosure cooling fan running
	FanReported bool

	HeaterActive   bool // enclosure heater running
	HeaterReported bool
}

func (DMSEnvironment) FacetKind() Kind { return KindDMSEnvironment }
