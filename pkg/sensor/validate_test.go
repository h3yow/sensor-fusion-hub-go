package sensor_test

import (
	"testing"

	"github.com/h3yow/sensor-fusion-hub-go/pkg/sensor"
)

func TestTemperatureNormalization(t *testing.T) {
	cases := []struct {
		u    sensor.Unit
		in   float64
		want float64
	}{
		{sensor.UnitCelsius, 10, 10},
		{sensor.UnitFahrenheit, 32, 0},
		{sensor.UnitFahrenheit, 212, 100},
		{sensor.UnitKelvin, 273.15, 0},
	}
	for _, c := range cases {
		s := sensor.Sample{Kind: sensor.KindTemperature, Unit: c.u, Frame: sensor.FrameThermal,
			M: sensor.Measurement{Temperature: c.in}}
		got := sensor.Normalize(s)
		if got.Unit != sensor.UnitCelsius || ddiff(got.M.Temperature, c.want) > 1e-9 {
			t.Fatalf("%v %v -> %v %v, want %v C", c.in, c.u, got.M.Temperature, got.Unit, c.want)
		}
	}
}

func TestIMUNormalization(t *testing.T) {
	// deg/s -> rad/s, g -> m/s^2.
	s := sensor.Sample{Kind: sensor.KindIMU, Unit: sensor.UnitDPS, Frame: sensor.FrameBody,
		M: sensor.Measurement{Gyro: sensor.Vec3{Z: 180}, Acc: sensor.Vec3{Z: 1}}}
	out := sensor.Normalize(s)
	if out.Unit != sensor.UnitRPS {
		t.Fatalf("unit = %v", out.Unit)
	}
	if ddiff(out.M.Gyro.Z, 3.141592653589793) > 1e-12 {
		t.Fatalf("gyro = %v", out.M.Gyro.Z)
	}
	if ddiff(out.M.Acc.Z, sensor.Gravity0) > 1e-9 {
		t.Fatalf("acc = %v", out.M.Acc.Z)
	}
}

func TestRegistrationCompatibility(t *testing.T) {
	if err := sensor.ValidateRegistration(sensor.KindTemperature, sensor.UnitCelsius, sensor.FrameThermal); err != nil {
		t.Fatal(err)
	}
	if err := sensor.ValidateRegistration(sensor.KindGPS, sensor.UnitECEF, sensor.FrameWGS84); err == nil {
		t.Fatal("ecef unit with wgs84 frame must conflict")
	}
	u, f, err := sensor.NormalizedUnitPair(sensor.KindGPS, sensor.UnitLLA, sensor.FrameWGS84)
	if err != nil || u != sensor.UnitLLA || f != sensor.FrameWGS84 {
		t.Fatalf("gps pair: %v %v %v", u, f, err)
	}
}

func ddiff(a, b float64) float64 {
	d := a - b
	if d < 0 {
		return -d
	}
	return d
}
