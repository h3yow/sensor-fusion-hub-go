package sensorhub

import "time"

// Event 是一次可观测的判定事件（时间线/依据/状态）。
type Event struct {
	At       time.Time
	SourceID string
	Phase    string
	Summary  string
	Status   string
	Reasons  []Reason
	Detail   map[string]any
}

// EventLog 是线程安全的事件日志。
type EventLog struct {
	events []Event
}

// NewEventLog 创建空事件日志。
func NewEventLog() *EventLog { return &EventLog{} }

func (l *EventLog) Append(e Event) {}

func (l *EventLog) Events() []Event { return nil }

// Lines 将事件渲染为人类可读的时间线行。
func (l *EventLog) Lines() []string { return nil }
