package calibrate_test

import (
	"errors"
	"math"
	"testing"

	"github.com/h3yow/sensor-fusion-hub-go/pkg/calibrate"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/sensor"
)

func TestRegistryEpochsAreMonotonicAndAppendOnly(t *testing.T) {
	r := calibrate.NewRegistry()
	if err := r.Register("s1", sensor.KindTemperature); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("s1", sensor.KindTemperature); !errors.Is(err, sensor.ErrDuplicateSource) {
		t.Fatalf("duplicate register: %v", err)
	}

	e1, err := r.Update("s1", calibrate.Params{Bias: [3]float64{-1, 0, 0}, Scale: [3]float64{1, 0, 0}})
	if err != nil || e1 != 1 {
		t.Fatalf("update1: e=%d err=%v", e1, err)
	}
	e2, err := r.Update("s1", calibrate.Params{Bias: [3]float64{-2, 0, 0}, Scale: [3]float64{1, 0, 0}})
	if err != nil || e2 != 2 {
		t.Fatalf("update2: e=%d err=%v", e2, err)
	}

	// History stays queryable: old params are never overwritten.
	p0, _ := r.ParamsAt("s1", 0)
	p1, _ := r.ParamsAt("s1", 1)
	p2, _ := r.ParamsAt("s1", 2)
	if p0.Bias[0] != 0 || p1.Bias[0] != -1 || p2.Bias[0] != -2 {
		t.Fatalf("history corrupted: %+v %+v %+v", p0, p1, p2)
	}
	if _, err := r.Update("ghost", calibrate.IdentityParams()); !errors.Is(err, sensor.ErrUnknownSource) {
		t.Fatalf("update unknown: %v", err)
	}
}

func TestApplyIsPureAndDeterministic(t *testing.T) {
	p := calibrate.Params{Bias: [3]float64{-1, -1, -1}, Scale: [3]float64{2, 2, 2}}
	in := sensor.Measurement{Temperature: 10,
		Gyro: sensor.Vec3{X: 1}, Acc: sensor.Vec3{Y: 2},
		Lat: 30, X: 100}
	a := calibrate.Apply(p, sensor.KindTemperature, in)
	b := calibrate.Apply(p, sensor.KindTemperature, in)
	if a.Temperature != 18 || a.Temperature != b.Temperature {
		t.Fatalf("temp cal = %v/%v", a.Temperature, b.Temperature)
	}
	gi := calibrate.Apply(p, sensor.KindIMU, in)
	if gi.Gyro.X != 0 || gi.Acc.Y != 2 {
		t.Fatalf("imu cal = %+v", gi)
	}
	gp := calibrate.Apply(p, sensor.KindGPS, in)
	if gp.Lat != 29 || gp.X != 99 {
		t.Fatalf("gps cal = %+v", gp)
	}
	if in.Temperature != 10 {
		t.Fatal("Apply must not mutate input")
	}
}

func TestValidation(t *testing.T) {
	if err := calibrate.Validate(sensor.KindTemperature, calibrate.Params{
		Bias: [3]float64{0, 0, 0}, Scale: [3]float64{1, 0, 0},
	}); err != nil {
		t.Fatalf("valid temp params rejected: %v", err)
	}
	if err := calibrate.Validate(sensor.KindTemperature, calibrate.Params{
		Bias: [3]float64{0, 1, 0}, Scale: [3]float64{1, 0, 0},
	}); err == nil {
		t.Fatal("multi-axis temp params must be rejected")
	}
	if err := calibrate.Validate(sensor.KindIMU, calibrate.Params{
		Scale: [3]float64{1, 1, math.NaN()},
	}); err == nil {
		t.Fatal("NaN scale must be rejected")
	}
	if err := calibrate.Validate(sensor.KindGPS, calibrate.Params{
		Scale: [3]float64{2, 1, 1},
	}); err == nil {
		t.Fatal("gps scale != 1 must be rejected")
	}
}
