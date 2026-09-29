package sensorhub

import "errors"

// DeviceConfig 描述设备接入配置与兼容规则。
type DeviceConfig struct {
	ID        string
	Kind      Kind
	Unit      Unit
	Frame     Frame
	Arity     int
	Precision float64 // 噪声标准差（用于融合权重）
}

// Registry 管理已接入设备的注册与配置校验。
type Registry struct {
	devices map[string]DeviceConfig
}

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) Register(cfg DeviceConfig) error { return errors.New("not implemented") }

func (r *Registry) Get(id string) (DeviceConfig, bool) { return DeviceConfig{}, false }

func (r *Registry) List() []DeviceConfig { return nil }
