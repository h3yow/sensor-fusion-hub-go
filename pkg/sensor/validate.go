package sensor

import "time"

// Gravity0 is the standard gravity used to convert "g" accelerometer units.
const Gravity0 = 9.80665

const g0 = Gravity0

// temperature-related constants.
const (
	kelvinOffset = 273.15
)

// CanonicalUnit returns the canonical unit a sample of the given kind is
// normalized to.
//
// Temperature -> Celsius.
// IMU         -> gyro rad/s + accelerometer m/s^2 (represented by UnitRPS;
//
//	the two sub-quantities always travel together).
//
// GPS         -> LLA (deg,deg,m) in WGS84, or ECEF meters. The declared
//
//	variant is preserved and two variants are not fused
//	together (see ValidateRegistration / hub config checks).
func CanonicalUnit(k Kind) Unit {
	switch k {
	case KindTemperature:
		return UnitCelsius
	case KindIMU:
		return UnitRPS
	case KindGPS:
		return UnitLLA
	default:
		return ""
	}
}

// CanonicalFrame returns the canonical coordinate frame for the given kind
// in its canonical unit variant.
func CanonicalFrame(k Kind) Frame {
	switch k {
	case KindTemperature:
		return FrameThermal
	case KindIMU:
		return FrameBody
	case KindGPS:
		return FrameWGS84
	}
	return ""
}

// supportedUnits lists every unit accepted per kind (canonical first).
var supportedUnits = map[Kind][]Unit{
	KindTemperature: {UnitCelsius, UnitFahrenheit, UnitKelvin},
	KindIMU:         {UnitRPS, UnitDPS},
	KindGPS:         {UnitLLA, UnitECEF},
}

// supportedFrames lists every frame accepted per kind.
var supportedFrames = map[Kind][]Frame{
	KindTemperature: {FrameThermal},
	KindIMU:         {FrameBody, FrameENU, FrameNED},
	KindGPS:         {FrameWGS84, FrameECEF},
}

func containsUnit(us []Unit, u Unit) bool {
	for _, x := range us {
		if x == u {
			return true
		}
	}
	return false
}

func containsFrame(fs []Frame, f Frame) bool {
	for _, x := range fs {
		if x == f {
			return true
		}
	}
	return false
}

// NormalizedUnitPair reports the canonical unit and frame that a source
// declaring (k,u,f) will be normalized into. It is the single place that
// defines compatibility between sources of the same kind: two sources of
// one kind are fusable only when their normalized pairs are identical.
func NormalizedUnitPair(k Kind, u Unit, f Frame) (Unit, Frame, error) {
	if err := ValidateRegistration(k, u, f); err != nil {
		return "", "", err
	}
	switch k {
	case KindTemperature:
		return UnitCelsius, FrameThermal, nil
	case KindIMU:
		// IMU sources are normalized as a pair: UnitRPS means
		// (rad/s, m/s^2); UnitDPS means (deg/s, g). The declared frame is
		// retained (rotating frames needs per-device extrinsics, which are
		// out of scope), so the frame participates in the pair.
		return UnitRPS, f, nil
	case KindGPS:
		if u == UnitECEF {
			return UnitECEF, FrameECEF, nil
		}
		return UnitLLA, FrameWGS84, nil
	}
	return "", "", ErrUnsupportedUnit
}

// ValidateRegistration checks a proposed source registration for kind/unit/
// frame support and internal consistency. It never mutates state.
func ValidateRegistration(k Kind, u Unit, f Frame) error {
	us, ok := supportedUnits[k]
	if !ok {
		return ErrUnsupportedUnit
	}
	if !containsUnit(us, u) {
		return ErrUnsupportedUnit
	}
	fs := supportedFrames[k]
	if !containsFrame(fs, f) {
		return ErrUnsupportedFrame
	}
	// Internal unit/frame consistency for GPS.
	switch k {
	case KindGPS:
		if u == UnitLLA && f != FrameWGS84 {
			return ErrConfigConflict
		}
		if u == UnitECEF && f != FrameECEF {
			return ErrConfigConflict
		}
	case KindTemperature:
		if f != FrameThermal {
			return ErrConfigConflict
		}
	}
	return nil
}

// Normalize converts a raw sample to canonical units/frames. It is a pure
// function and does not touch source state.
func Normalize(s Sample) NormalizedSample {
	out := s
	switch s.Kind {
	case KindTemperature:
		switch s.Unit {
		case UnitFahrenheit:
			out.M.Temperature = (s.M.Temperature - 32.0) * 5.0 / 9.0
		case UnitKelvin:
			out.M.Temperature = s.M.Temperature - kelvinOffset
		}
		out.Unit = UnitCelsius
		out.Frame = FrameThermal
	case KindIMU:
		if s.Unit == UnitDPS {
			out.M.Gyro = scaleVec(s.M.Gyro, deg2rad)
			out.M.Acc = scaleVec(s.M.Acc, g0)
		}
		out.Unit = UnitRPS
		// frame intentionally preserved
	case KindGPS:
		if s.Unit == UnitECEF {
			out.Unit = UnitECEF
			out.Frame = FrameECEF
		} else {
			out.Unit = UnitLLA
			out.Frame = FrameWGS84
		}
	}
	return NormalizedSample{Sample: out}
}

const deg2rad = 0.017453292519943295

func scaleVec(v Vec3, f float64) Vec3 {
	return Vec3{X: v.X * f, Y: v.Y * f, Z: v.Z * f}
}

// FreshnessWindow is the default per-kind freshness window used to decide
// whether the latest sample of a source still counts as current.
func FreshnessWindow(k Kind) time.Duration {
	switch k {
	case KindTemperature:
		return 2 * time.Second
	case KindIMU:
		return time.Second
	case KindGPS:
		return 2 * time.Second
	}
	return time.Second
}
