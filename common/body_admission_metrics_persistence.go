package common

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const requestBodyMetricsStateVersion = 1

type requestBodyMetricsState struct {
	Version   int              `json:"version"`
	UpdatedAt time.Time        `json:"updated_at"`
	Stats     RequestBodyStats `json:"stats"`
}

// RequestBodyMetricsPersistenceStatus is exposed on the root-only performance
// endpoint so operators can verify that the in-process counters are actually
// being checkpointed instead of silently resetting on every restart.
type RequestBodyMetricsPersistenceStatus struct {
	Enabled          bool       `json:"enabled"`
	StatePath        string     `json:"state_path,omitempty"`
	Restored         bool       `json:"restored"`
	LastCheckpointAt *time.Time `json:"last_checkpoint_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
}

var requestBodyMetricsPersistence struct {
	sync.RWMutex
	started          atomic.Bool
	statePath        string
	restored         bool
	lastCheckpointAt *time.Time
	lastError        string
}

var requestBodyMetricsCheckpointMu sync.Mutex

func defaultRequestBodyMetricsStatePath() string {
	if configured := strings.TrimSpace(os.Getenv("REQUEST_BODY_METRICS_STATE_PATH")); configured != "" {
		return configured
	}
	if IsRunningInContainer() {
		return "/data/request-body-metrics-v1.json"
	}
	databasePath := strings.SplitN(SQLitePath, "?", 2)[0]
	if databasePath == "" {
		return "request-body-metrics-v1.json"
	}
	return filepath.Join(filepath.Dir(databasePath), "request-body-metrics-v1.json")
}

// StartRequestBodyMetricsPersistence restores the most recent checkpoint
// synchronously, then starts a lightweight periodic checkpoint loop. The
// active-slot gauge is intentionally never restored because no lease survives
// a process restart.
func StartRequestBodyMetricsPersistence() {
	if !requestBodyMetricsPersistence.started.CompareAndSwap(false, true) {
		return
	}

	requestBodyMetricsPersistence.Lock()
	requestBodyMetricsPersistence.statePath = defaultRequestBodyMetricsStatePath()
	statePath := requestBodyMetricsPersistence.statePath
	requestBodyMetricsPersistence.Unlock()

	if GetPerformanceMonitorConfig().RequestBodyMetricsPersistenceEnabled {
		restored, err := loadRequestBodyMetrics(statePath)
		setRequestBodyMetricsPersistenceResult(restored, nil, err)
		if err != nil {
			SysError(fmt.Sprintf("failed to restore request body metrics: %s", err.Error()))
		} else if restored {
			SysLog("request body metrics restored from persistent checkpoint")
		}
	}

	go func() {
		for {
			config := GetPerformanceMonitorConfig()
			intervalSeconds := config.RequestBodyMetricsPersistenceIntervalSecs
			if intervalSeconds <= 0 {
				intervalSeconds = 30
			}
			time.Sleep(time.Duration(intervalSeconds) * time.Second)
			if !config.RequestBodyMetricsPersistenceEnabled {
				continue
			}
			if err := CheckpointRequestBodyMetrics(); err != nil {
				SysError(fmt.Sprintf("failed to checkpoint request body metrics: %s", err.Error()))
			}
		}
	}()
}

// CheckpointRequestBodyMetrics persists the current cumulative counters. It is
// safe to call during graceful shutdown and after an explicit stats reset.
func CheckpointRequestBodyMetrics() error {
	if !requestBodyMetricsPersistence.started.Load() || !GetPerformanceMonitorConfig().RequestBodyMetricsPersistenceEnabled {
		return nil
	}

	requestBodyMetricsPersistence.RLock()
	statePath := requestBodyMetricsPersistence.statePath
	requestBodyMetricsPersistence.RUnlock()
	if statePath == "" {
		return nil
	}

	err := saveRequestBodyMetrics(statePath, GetRequestBodyStats())
	var checkpointAt *time.Time
	if err == nil {
		checkpointAt = timePointer(time.Now())
	}
	setRequestBodyMetricsPersistenceResult(false, checkpointAt, err)
	return err
}

func GetRequestBodyMetricsPersistenceStatus() RequestBodyMetricsPersistenceStatus {
	requestBodyMetricsPersistence.RLock()
	defer requestBodyMetricsPersistence.RUnlock()

	status := RequestBodyMetricsPersistenceStatus{
		Enabled:   requestBodyMetricsPersistence.started.Load() && GetPerformanceMonitorConfig().RequestBodyMetricsPersistenceEnabled,
		StatePath: requestBodyMetricsPersistence.statePath,
		Restored:  requestBodyMetricsPersistence.restored,
		LastError: requestBodyMetricsPersistence.lastError,
	}
	if requestBodyMetricsPersistence.lastCheckpointAt != nil {
		checkpointAt := *requestBodyMetricsPersistence.lastCheckpointAt
		status.LastCheckpointAt = &checkpointAt
	}
	return status
}

func setRequestBodyMetricsPersistenceResult(restored bool, checkpointAt *time.Time, err error) {
	requestBodyMetricsPersistence.Lock()
	defer requestBodyMetricsPersistence.Unlock()
	if restored {
		requestBodyMetricsPersistence.restored = true
	}
	if checkpointAt != nil {
		checkpointCopy := *checkpointAt
		requestBodyMetricsPersistence.lastCheckpointAt = &checkpointCopy
	}
	if err != nil {
		requestBodyMetricsPersistence.lastError = err.Error()
	} else {
		requestBodyMetricsPersistence.lastError = ""
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func saveRequestBodyMetrics(path string, stats RequestBodyStats) error {
	requestBodyMetricsCheckpointMu.Lock()
	defer requestBodyMetricsCheckpointMu.Unlock()

	if strings.TrimSpace(path) == "" {
		return errors.New("request body metrics state path is empty")
	}
	stats.ActiveLargeBodySlots = 0
	state := requestBodyMetricsState{
		Version:   requestBodyMetricsStateVersion,
		UpdatedAt: time.Now().UTC(),
		Stats:     stats,
	}
	data, err := Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal request body metrics state: %w", err)
	}

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0750); err != nil {
		return fmt.Errorf("create request body metrics directory: %w", err)
	}
	temporaryFile, err := os.CreateTemp(directory, ".request-body-metrics-*.tmp")
	if err != nil {
		return fmt.Errorf("create request body metrics checkpoint: %w", err)
	}
	temporaryPath := temporaryFile.Name()
	defer func() {
		_ = temporaryFile.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporaryFile.Chmod(0600); err != nil {
		return fmt.Errorf("protect request body metrics checkpoint: %w", err)
	}
	if _, err := temporaryFile.Write(data); err != nil {
		return fmt.Errorf("write request body metrics checkpoint: %w", err)
	}
	if err := temporaryFile.Sync(); err != nil {
		return fmt.Errorf("sync request body metrics checkpoint: %w", err)
	}
	if err := temporaryFile.Close(); err != nil {
		return fmt.Errorf("close request body metrics checkpoint: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace request body metrics checkpoint: %w", err)
	}
	return nil
}

func loadRequestBodyMetrics(path string) (bool, error) {
	requestBodyMetricsCheckpointMu.Lock()
	defer requestBodyMetricsCheckpointMu.Unlock()

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read request body metrics checkpoint: %w", err)
	}
	var state requestBodyMetricsState
	if err := Unmarshal(data, &state); err != nil {
		return false, fmt.Errorf("decode request body metrics checkpoint: %w", err)
	}
	if state.Version != requestBodyMetricsStateVersion {
		return false, fmt.Errorf("unsupported request body metrics checkpoint version %d", state.Version)
	}
	restoreRequestBodyStats(state.Stats)
	return true, nil
}

func restoreRequestBodyStats(stats RequestBodyStats) {
	requestBodyMetrics.rejectedLargeBodies.Store(nonNegativeInt64(stats.RejectedLargeBodyAdmissionsTotal))
	requestBodyMetrics.observedBodies.Store(nonNegativeInt64(stats.ObservedBodiesTotal))
	requestBodyMetrics.observedBodyBytes.Store(nonNegativeInt64(stats.ObservedBodyBytesTotal))
	requestBodyMetrics.maxObservedBodyBytes.Store(nonNegativeInt64(stats.MaxObservedBodyBytes))

	bodyBuckets := [...]int64{
		stats.RequestBodySizeBuckets.LE1MiB,
		stats.RequestBodySizeBuckets.LE4MiB,
		stats.RequestBodySizeBuckets.LE16MiB,
		stats.RequestBodySizeBuckets.LE64MiB,
		stats.RequestBodySizeBuckets.LE256MiB,
	}
	for index, value := range bodyBuckets {
		requestBodyMetrics.bodySizeBuckets[index].Store(nonNegativeInt64(value))
	}

	requestBodyMetrics.admissionHolds.Store(nonNegativeInt64(stats.AdmissionHoldsTotal))
	requestBodyMetrics.admissionHoldNanoseconds.Store(secondsToNanoseconds(stats.AdmissionHoldDurationSecondsTotal))
	requestBodyMetrics.maxAdmissionHoldNanoseconds.Store(secondsToNanoseconds(stats.AdmissionHoldDurationSecondsMax))
	holdBuckets := [...]int64{
		stats.AdmissionHoldDurationBuckets.LE10Milliseconds,
		stats.AdmissionHoldDurationBuckets.LE100Milliseconds,
		stats.AdmissionHoldDurationBuckets.LE1Second,
		stats.AdmissionHoldDurationBuckets.LE10Seconds,
	}
	for index, value := range holdBuckets {
		requestBodyMetrics.admissionHoldBuckets[index].Store(nonNegativeInt64(value))
	}
	requestBodyMetrics.activeLargeBodySlots.Store(0)
}

func nonNegativeInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func secondsToNanoseconds(seconds float64) int64 {
	if seconds <= 0 || math.IsNaN(seconds) {
		return 0
	}
	if seconds >= float64(math.MaxInt64)/float64(time.Second) {
		return math.MaxInt64
	}
	return int64(math.Round(seconds * float64(time.Second)))
}
