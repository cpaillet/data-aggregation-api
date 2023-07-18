package job

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/criteo/data-aggregation-api/internal/api/router"
	"github.com/criteo/data-aggregation-api/internal/config"
	"github.com/criteo/data-aggregation-api/internal/convertor/device"
	"github.com/criteo/data-aggregation-api/internal/ingestor/repository"
	"github.com/criteo/data-aggregation-api/internal/metrics"
	"github.com/criteo/data-aggregation-api/internal/report"
)

// Precompute prepares data to ease compute per device.
// The goal is to copy data to each device to be able to build devices independently.
func precompute(reportCh chan report.Message, ingestorRepo *repository.Assets) (map[string]*device.Device, error) {
	log.Info().Msg("start precompute")
	devicesData := ingestorRepo.Precompute()
	var devices = make(map[string]*device.Device)
	var allPrecomputeErrors error

	for _, dev := range ingestorRepo.DeviceInventory {
		if newDevice, err := device.NewDevice(dev, devicesData); err != nil {
			devices[dev.Hostname] = nil
			reportCh <- report.Message{
				Type:     report.PrecomputeMessage,
				Severity: report.Error,
				Text:     err.Error(),
			}
			allPrecomputeErrors = errors.Join(allPrecomputeErrors, err)
		} else {
			devices[dev.Hostname] = newDevice
		}
	}

	return devices, allPrecomputeErrors
}

// Compute generates OpenConfig data for each device.
func compute(reportCh chan<- report.Message, ingestorRepo *repository.Assets, devices map[string]*device.Device) error {
	wg := sync.WaitGroup{}

	failed := false
	var mutex sync.Mutex

	for _, dev := range ingestorRepo.DeviceInventory {
		if devices[dev.Hostname] == nil {
			reportCh <- report.Message{
				Type:     report.ComputeMessage,
				Severity: report.Warning,
				Text:     fmt.Sprintf("device %s has no configuration", dev.Hostname),
			}
			continue
		}

		wg.Add(1)
		go func(dev *device.Device) {
			defer wg.Done()
			if err := dev.GenerateOpenconfig(); err != nil {
				reportCh <- report.Message{
					Type:     report.PrecomputeMessage,
					Severity: report.Error,
					Text:     err.Error(),
				}
				mutex.Lock()
				failed = true
				mutex.Unlock()
			}
		}(devices[dev.Hostname])
	}

	wg.Wait()
	if failed {
		return errors.New("OpenConfig conversion failed")
	}

	return nil
}

// RunBuild start the build pipeline to convert CMDB data to OpenConfig for each devices.
// One build is composed are three steps:
//   - fetch data using ingestors (one ingestor = one data source API endpoint)
//   - precompute data to make them usable
//   - compute to OpenConfig
func RunBuild(reportCh chan report.Message) (map[string]*device.Device, report.PerformanceStats, error) {
	stats := report.PerformanceStats{}
	startTime := time.Now()

	// Fetch data from CMDB
	ingestorRepo, err := repository.FetchAssets(reportCh)
	if err != nil {
		return nil, stats, err
	}
	ingestorRepo.PrintStats()
	ingestorRepo.ReportStats(reportCh)
	ingestorFetchFinishTime := time.Now()
	stats.DataFetchingDuration = ingestorFetchFinishTime.Sub(startTime)

	// Precompute data per device
	devices, precomputeError := precompute(reportCh, ingestorRepo)
	precomputeFinishTime := time.Now()
	stats.PrecomputeDuration = precomputeFinishTime.Sub(ingestorFetchFinishTime)

	// We stop here if the user decided all device configuration must have been built with success
	if precomputeError != nil {
		if config.Cfg.Build.AllDevicesMustBuild {
			return nil, stats, errors.New("failed: all devices must build")
		}
		reportCh <- report.Message{
			Type:     report.ComputeMessage,
			Severity: report.Warning,
			Text:     "build failed for some devices",
		}
	}

	// Generate openconfig for all devices
	computeError := compute(reportCh, ingestorRepo, devices)
	computeTime := time.Now()
	stats.ComputeDuration = computeTime.Sub(precomputeFinishTime)
	stats.BuildDuration = computeTime.Sub(startTime)

	stats.Log()

	if computeError != nil {
		return nil, stats, computeError
	}

	return devices, stats, nil
}

// StartBuildLoop starts the build in an infinite loop.
func StartBuildLoop(deviceRepo router.DevicesRepository, reports *report.Repository) {
	metricsRegistry := metrics.NewRegistry()
	for {
		var wg sync.WaitGroup
		reports.StartNewReport()
		var reportCh = make(chan report.Message, 1)

		wg.Add(1)
		go func() {
			defer wg.Done()
			reports.Watch(reportCh)
		}()

		reports.UpdateStatus(report.InProgress)
		if devs, stats, err := RunBuild(reportCh); err != nil {
			metricsRegistry.BuildFailed()
			reports.UpdateStatus(report.Failed)
			reports.UpdatePerformanceStats(stats)
			log.Error().Err(err).Msg("build failed")
		} else {
			deviceRepo.Set(devs)
			log.Info().Msg("build successful")
			metricsRegistry.BuildSuccessful()
			reports.UpdateStatus(report.Success)
			reports.UpdatePerformanceStats(stats)
			reports.MarkAsSuccessful()
		}

		reports.MarkAsComplete()
		close(reportCh)
		wg.Wait()

		time.Sleep(config.Cfg.Build.Interval)
	}
}
