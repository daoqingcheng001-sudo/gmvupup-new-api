package service

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

const (
	requestBodyAdmissionMonitorInterval = 5 * time.Second
	requestBodyHighUsageRequiredSamples = 3
)

type requestBodyCapacityAlert struct {
	Title   string
	Content string
}

var startRequestBodyAdmissionMonitorOnce sync.Once

// StartRequestBodyAdmissionMonitor actively watches the same counters exposed
// by the root performance endpoint. Rejections alert immediately; sustained
// slot pressure alerts after three samples to avoid transient noise.
func StartRequestBodyAdmissionMonitor() {
	startRequestBodyAdmissionMonitorOnce.Do(func() {
		go monitorRequestBodyAdmissionCapacity()
	})
}

func monitorRequestBodyAdmissionCapacity() {
	previous := common.GetRequestBodyStats()
	highUsageSamples := 0
	var lastNotificationAt time.Time
	ticker := time.NewTicker(requestBodyAdmissionMonitorInterval)
	defer ticker.Stop()

	for now := range ticker.C {
		current := common.GetRequestBodyStats()
		config := common.GetPerformanceMonitorConfig()
		if !config.RequestBodyAlertEnabled {
			previous = current
			highUsageSamples = 0
			continue
		}

		alert, nextHighUsageSamples := evaluateRequestBodyCapacity(
			previous,
			current,
			highUsageSamples,
			config.RequestBodyAlertSlotUsageThreshold,
		)
		highUsageSamples = nextHighUsageSamples
		previous = current
		if alert == nil {
			continue
		}

		cooldownSeconds := config.RequestBodyAlertCooldownSeconds
		if cooldownSeconds <= 0 {
			cooldownSeconds = 600
		}
		if !lastNotificationAt.IsZero() && now.Sub(lastNotificationAt) < time.Duration(cooldownSeconds)*time.Second {
			continue
		}
		lastNotificationAt = now
		common.SysError("request body capacity alert: " + alert.Content)
		NotifyRootUser(dto.NotifyTypeRequestBodyCapacity, alert.Title, alert.Content)
	}
}

func evaluateRequestBodyCapacity(previous common.RequestBodyStats, current common.RequestBodyStats, highUsageSamples int, thresholdPercent int) (*requestBodyCapacityAlert, int) {
	if thresholdPercent <= 0 || thresholdPercent > 100 {
		thresholdPercent = 80
	}

	rejectedDelta := current.RejectedLargeBodyAdmissionsTotal - previous.RejectedLargeBodyAdmissionsTotal
	if rejectedDelta < 0 {
		// An operator reset the counters; do not turn the reset into an alert.
		rejectedDelta = 0
	}

	if requestBodySlotUsageAtLeast(current, thresholdPercent) {
		highUsageSamples++
	} else {
		highUsageSamples = 0
	}

	nodeName := strings.TrimSpace(common.NodeName)
	if nodeName == "" {
		nodeName = "new-api"
	}
	if rejectedDelta > 0 {
		return &requestBodyCapacityAlert{
			Title: fmt.Sprintf("[%s] 大请求体容量拒绝告警", nodeName),
			Content: fmt.Sprintf(
				"节点 %s 在最近 %s 内新增 %d 次大请求体容量拒绝；当前槽位 %d/%d，累计拒绝 %d。请检查请求体解析压力、内存与客户重试情况。",
				nodeName,
				requestBodyAdmissionMonitorInterval,
				rejectedDelta,
				current.ActiveLargeBodySlots,
				current.LargeBodySlotLimit,
				current.RejectedLargeBodyAdmissionsTotal,
			),
		}, highUsageSamples
	}
	if highUsageSamples >= requestBodyHighUsageRequiredSamples {
		return &requestBodyCapacityAlert{
			Title: fmt.Sprintf("[%s] 大请求体槽位高占用告警", nodeName),
			Content: fmt.Sprintf(
				"节点 %s 的大请求体槽位已连续 %d 次采样达到 %d%% 阈值；当前槽位 %d/%d。请检查请求体读取耗时和并发突增。",
				nodeName,
				requestBodyHighUsageRequiredSamples,
				thresholdPercent,
				current.ActiveLargeBodySlots,
				current.LargeBodySlotLimit,
			),
		}, highUsageSamples
	}
	return nil, highUsageSamples
}

func requestBodySlotUsageAtLeast(stats common.RequestBodyStats, thresholdPercent int) bool {
	if stats.LargeBodySlotLimit <= 0 || stats.ActiveLargeBodySlots <= 0 {
		return false
	}
	return stats.ActiveLargeBodySlots*100 >= stats.LargeBodySlotLimit*int64(thresholdPercent)
}
