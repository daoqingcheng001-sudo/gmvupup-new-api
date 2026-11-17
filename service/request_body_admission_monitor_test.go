package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestEvaluateRequestBodyCapacityAlertsOnNewRejection(t *testing.T) {
	previous := common.RequestBodyStats{
		RejectedLargeBodyAdmissionsTotal: 10,
		LargeBodySlotLimit:               256,
	}
	current := common.RequestBodyStats{
		RejectedLargeBodyAdmissionsTotal: 12,
		ActiveLargeBodySlots:             201,
		LargeBodySlotLimit:               256,
	}

	alert, highUsageSamples := evaluateRequestBodyCapacity(previous, current, 0, 80)

	require.NotNil(t, alert)
	require.Contains(t, alert.Content, "新增 2 次")
	require.Equal(t, 0, highUsageSamples)
}

func TestEvaluateRequestBodyCapacityAlertsOnlyAfterSustainedHighUsage(t *testing.T) {
	stats := common.RequestBodyStats{
		ActiveLargeBodySlots: 205,
		LargeBodySlotLimit:   256,
	}

	var alert *requestBodyCapacityAlert
	highUsageSamples := 0
	for sample := 1; sample <= requestBodyHighUsageRequiredSamples; sample++ {
		alert, highUsageSamples = evaluateRequestBodyCapacity(stats, stats, highUsageSamples, 80)
		if sample < requestBodyHighUsageRequiredSamples {
			require.Nil(t, alert)
		}
	}
	require.NotNil(t, alert)
	require.Contains(t, alert.Content, "205/256")

	alert, highUsageSamples = evaluateRequestBodyCapacity(stats, stats, highUsageSamples, 80)
	require.NotNil(t, alert, "sustained pressure remains alertable after the notification cooldown")
	require.Equal(t, requestBodyHighUsageRequiredSamples+1, highUsageSamples)
}

func TestEvaluateRequestBodyCapacityDoesNotAlertAfterCounterReset(t *testing.T) {
	previous := common.RequestBodyStats{RejectedLargeBodyAdmissionsTotal: 20, LargeBodySlotLimit: 256}
	current := common.RequestBodyStats{RejectedLargeBodyAdmissionsTotal: 0, LargeBodySlotLimit: 256}

	alert, highUsageSamples := evaluateRequestBodyCapacity(previous, current, 0, 80)

	require.Nil(t, alert)
	require.Zero(t, highUsageSamples)
}
