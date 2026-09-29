package sensorhub

// Calibration 描述某一版本的线性校准参数：y = gain*x + bias。
type Calibration struct {
	Version     uint64
	EffectiveAt int64 // UnixNano
	Gain        []float64
	Bias        []float64
}

// CalibrationStore 按源保存带生效时间的校准版本链。
type CalibrationStore struct{}

func NewCalibrationStore() *CalibrationStore { return &CalibrationStore{} }

func (s *CalibrationStore) Update(sourceID string, c Calibration) error { return nil }

func (s *CalibrationStore) VersionAt(sourceID string, tsNano int64) (Calibration, bool) {
	return Calibration{}, false
}

func (s *CalibrationStore) Latest(sourceID string) (Calibration, bool) {
	return Calibration{}, false
}
