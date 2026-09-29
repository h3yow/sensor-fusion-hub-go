// Command sfdemo runs a fully local, deterministic demonstration:
// simulated temperature/IMU sources, a loss burst, low quality, a device
// failure/restart, a calibration update and periodic fused snapshots.
// No real hardware or external service is used; a scripted local clock
// drives every timestamp, so repeated runs produce identical output.
package main

import (
	"fmt"
	"time"

	"github.com/h3yow/sensor-fusion-hub-go/pkg/calibrate"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/clock"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/engine"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/hub"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/logging"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/sensor"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/source"
)

type planned struct {
	src sensor.SourceID
	sa  sensor.Sample
}

func main() {
	t0 := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	log := logging.New(nil)

	clk := clock.NewScriptedClock(steps(t0))
	clk.Start()

	cfg := hub.Config{
		Sources: []source.Descriptor{
			{ID: "temp-a", Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal, Address: "sim:1", Period: time.Second},
			{ID: "temp-b", Kind: sensor.KindTemperature, Unit: sensor.UnitFahrenheit, Frame: sensor.FrameThermal, Address: "sim:2", Period: time.Second},
			{ID: "imu-1", Kind: sensor.KindIMU, Unit: sensor.UnitDPS, Frame: sensor.FrameBody, Address: "sim:3", Period: time.Second},
		},
		Tuning: engine.DefaultTuning(),
	}

	h, err := hub.New(cfg, clk, log)
	if err != nil {
		panic(err)
	}

	tempA, _ := source.NewSimSource(source.Options{Desc: cfg.Sources[0], Clock: clk})
	tempB, _ := source.NewSimSource(source.Options{Desc: cfg.Sources[1], Clock: clk})
	imu1, _ := source.NewSimSource(source.Options{Desc: cfg.Sources[2], Clock: clk})
	// Timeline: t+1 normal; t+2 gap for temp-a (seq jumps 2->4);
	// t+3 low-quality temp-b, then calibration update; t+4 temp-b fails;
	// t+5 IMU restart (epoch bump, seq resets) + temp-b recovered.
	timeline := map[int][]planned{
		1: {
			{"temp-a", temp(tempA, 1, 0, 20.0, 1)},
			{"temp-b", temp(tempB, 1, 0, 69.0, 1)},
			{"imu-1", imuSample(imu1, 1, 0, 0.01, 1.00, 1)},
		},
		2: {
			{"temp-a", temp(tempA, 2, 0, 20.5, 1)},
			{"temp-b", temp(tempB, 2, 0, 69.5, 1)},
			{"imu-1", imuSample(imu1, 2, 0, 0.01, 1.00, 1)},
		},
		3: {
			// seq 3 of temp-a is missing -> gap
			{"temp-a", temp(tempA, 4, 0, 21.5, 1)},
			{"temp-b", temp(tempB, 3, 0, 70.0, 0.1)},
			{"imu-1", imuSample(imu1, 3, 0, 0.01, 1.00, 1)},
		},
		4: {
			{"temp-a", temp(tempA, 5, 0, 22.0, 1)},
			{"temp-b", temp(tempB, 4, 0, 70.5, 1)},
			{"imu-1", imuSample(imu1, 4, 0, 0.02, 1.00, 1)},
		},
		5: {
			{"temp-a", temp(tempA, 6, 0, 22.2, 1)},
			{"imu-1", imuSample(imu1, 1, 1, 0.02, 1.00, 1)},
		},
		6: {
			{"temp-a", temp(tempA, 7, 0, 22.3, 1)},
			{"temp-b", temp(tempB, 5, 1, 71.0, 1)},
			{"imu-1", imuSample(imu1, 2, 1, 0.02, 1.00, 1)},
		},
	}

	for i := 1; i <= 6; i++ {
		clk.Set(t0.Add(time.Duration(i) * time.Second))
		for _, p := range timeline[i] {
			if p.src == "temp-b" && i == 5 {
				continue // device failed: nothing emitted
			}
			if err := h.Ingest(p.sa); err != nil {
				log.Warn("demo: ingest returned", "id", p.src, "err", err.Error())
			}
		}
		// Apply a new calibration epoch for temp-a right after the t+3
		// samples were ingested: t+3 stays on epoch 0, t+4+ on epoch 1.
		if i == 3 {
			if e, err := h.UpdateCalibration("temp-a", calibrate.Params{
				Bias:  [3]float64{-0.2, 0, 0},
				Scale: [3]float64{1, 0, 0},
			}); err != nil || e != 1 {
				panic("calibration update failed")
			}
		}
		if i == 5 {
			// Simulate observed device failure for temp-b.
			tempB.Fail()
		}
		if i == 6 {
			tempB.Restart()
		}
		printSnapshot(h.Snapshot())
	}
}

func temp(s *source.SimSource, seq, epoch uint64, v, q float64) sensor.Sample {
	d := s.Descriptor()
	return sensor.Sample{
		Source: d.ID, Kind: d.Kind, Unit: d.Unit, Frame: d.Frame,
		Seq: seq, Epoch: epoch, Quality: q,
		M: sensor.Measurement{Temperature: v},
	}
}

func imuSample(s *source.SimSource, seq, epoch uint64, gzDps, zg, q float64) sensor.Sample {
	d := s.Descriptor()
	return sensor.Sample{
		Source: d.ID, Kind: d.Kind, Unit: d.Unit, Frame: d.Frame,
		Seq: seq, Epoch: epoch, Quality: q,
		M: sensor.Measurement{Gyro: sensor.Vec3{Z: gzDps}, Acc: sensor.Vec3{Z: zg}},
	}
}

func steps(t0 time.Time) []clock.Step {
	var st []clock.Step
	for i := 0; i < 7; i++ {
		st = append(st, clock.Step{At: t0.Add(time.Duration(i) * time.Second), Tag: fmt.Sprintf("t+%ds", i)})
	}
	return st
}

func printSnapshot(s sensor.Snapshot) {
	fmt.Printf("--- snapshot at %s ---\n", s.TakenAt.Format(time.RFC3339))
	for _, st := range s.Sources {
		fmt.Printf("src=%-7s status=%-9s reason=%-18s seq=%d gaps=%d late=%d\n",
			st.ID, st.Status, st.Reason, st.LastSeq, st.GapCount, st.LateCount)
	}
	for _, f := range s.Fused {
		names := ""
		for i, c := range f.Contribs {
			if i > 0 {
				names += ","
			}
			names += string(c)
		}
		switch f.Kind {
		case sensor.KindTemperature:
			fmt.Printf("fused temp=%.4fC status=%-9s reason=%-18s from=[%s] calEpoch=%d\n",
				f.M.Temperature, f.Status, f.Reason, names, f.CalEpoch)
		case sensor.KindIMU:
			fmt.Printf("fused imu  gyro.z=%.5f acc.z=%.4f status=%-9s reason=%-18s from=[%s] calEpoch=%d\n",
				f.M.Gyro.Z, f.M.Acc.Z, f.Status, f.Reason, names, f.CalEpoch)
		}
	}
}
