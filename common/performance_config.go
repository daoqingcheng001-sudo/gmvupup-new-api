package common

import "sync/atomic"

// PerformanceMonitorConfig 性能监控配置
type PerformanceMonitorConfig struct {
	Enabled                                   bool
	CPUThreshold                              int
	MemoryThreshold                           int
	DiskThreshold                             int
	RequestBodyAlertEnabled                   bool
	RequestBodyAlertSlotUsageThreshold        int
	RequestBodyAlertCooldownSeconds           int
	RequestBodyMetricsPersistenceEnabled      bool
	RequestBodyMetricsPersistenceIntervalSecs int
}

var performanceMonitorConfig atomic.Value

func init() {
	// 初始化默认配置
	performanceMonitorConfig.Store(PerformanceMonitorConfig{
		Enabled:                                   true,
		CPUThreshold:                              90,
		MemoryThreshold:                           90,
		DiskThreshold:                             90,
		RequestBodyAlertEnabled:                   false,
		RequestBodyAlertSlotUsageThreshold:        80,
		RequestBodyAlertCooldownSeconds:           600,
		RequestBodyMetricsPersistenceEnabled:      true,
		RequestBodyMetricsPersistenceIntervalSecs: 30,
	})
}

// GetPerformanceMonitorConfig 获取性能监控配置
func GetPerformanceMonitorConfig() PerformanceMonitorConfig {
	return performanceMonitorConfig.Load().(PerformanceMonitorConfig)
}

// SetPerformanceMonitorConfig 设置性能监控配置
func SetPerformanceMonitorConfig(config PerformanceMonitorConfig) {
	performanceMonitorConfig.Store(config)
}
