package sensorhub

import "time"

// FusionResult 是一次融合输出。
type FusionResult struct {
	Kind     Kind
	Values   []float64
	Frame    Frame
	Status   OutputStatus
	Reasons  []Reason
	At       time.Time
	Used     []string
}

// Fuser 对同一 Kind 的多源读数做加权融合与降级判定。
type Fuser struct{}

func NewFuser(log *EventLog, clock Clock) *Fuser { return &Fuser{} }

func (f *Fuser) Fuse(snap Snapshot, kind Kind) FusionResult { return FusionResult{} }
