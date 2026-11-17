package common

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestBodyMetricsCheckpointRoundTripDoesNotRestoreActiveLeases(t *testing.T) {
	ResetRequestBodyStats()
	t.Cleanup(ResetRequestBodyStats)

	want := RequestBodyStats{
		ActiveLargeBodySlots:             7,
		RejectedLargeBodyAdmissionsTotal: 13,
		ObservedBodiesTotal:              21,
		ObservedBodyBytesTotal:           123456,
		MaxObservedBodyBytes:             65432,
		RequestBodySizeBuckets: RequestBodySizeBuckets{
			LE1MiB:   1,
			LE4MiB:   2,
			LE16MiB:  3,
			LE64MiB:  4,
			LE256MiB: 5,
		},
		AdmissionHoldsTotal:               8,
		AdmissionHoldDurationSecondsTotal: 2.5,
		AdmissionHoldDurationSecondsMax:   1.25,
		AdmissionHoldDurationBuckets: AdmissionHoldDurationBuckets{
			LE10Milliseconds:  1,
			LE100Milliseconds: 2,
			LE1Second:         3,
			LE10Seconds:       4,
		},
	}
	restoreRequestBodyStats(want)

	statePath := filepath.Join(t.TempDir(), "request-body-metrics.json")
	require.NoError(t, saveRequestBodyMetrics(statePath, GetRequestBodyStats()))
	ResetRequestBodyStats()
	restored, err := loadRequestBodyMetrics(statePath)
	require.NoError(t, err)
	require.True(t, restored)

	got := GetRequestBodyStats()
	require.Zero(t, got.ActiveLargeBodySlots, "in-flight leases must not survive process restart")
	require.Equal(t, want.RejectedLargeBodyAdmissionsTotal, got.RejectedLargeBodyAdmissionsTotal)
	require.Equal(t, want.ObservedBodiesTotal, got.ObservedBodiesTotal)
	require.Equal(t, want.ObservedBodyBytesTotal, got.ObservedBodyBytesTotal)
	require.Equal(t, want.MaxObservedBodyBytes, got.MaxObservedBodyBytes)
	require.Equal(t, want.RequestBodySizeBuckets, got.RequestBodySizeBuckets)
	require.Equal(t, want.AdmissionHoldsTotal, got.AdmissionHoldsTotal)
	require.InDelta(t, want.AdmissionHoldDurationSecondsTotal, got.AdmissionHoldDurationSecondsTotal, 0.000000001)
	require.InDelta(t, want.AdmissionHoldDurationSecondsMax, got.AdmissionHoldDurationSecondsMax, 0.000000001)
	require.Equal(t, want.AdmissionHoldDurationBuckets, got.AdmissionHoldDurationBuckets)
}

func TestRequestBodyMetricsCheckpointRejectsUnknownVersion(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "request-body-metrics.json")
	require.NoError(t, saveRequestBodyMetrics(statePath, RequestBodyStats{}))

	data, err := os.ReadFile(statePath)
	require.NoError(t, err)
	var state requestBodyMetricsState
	require.NoError(t, Unmarshal(data, &state))
	state.Version++
	data, err = Marshal(state)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(statePath, data, 0600))

	restored, err := loadRequestBodyMetrics(statePath)
	require.False(t, restored)
	require.ErrorContains(t, err, "unsupported request body metrics checkpoint version")
}
