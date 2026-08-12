package systemmetrics

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const cpuPowerSampleInterval = 2 * time.Second

type CPUSnapshot struct {
	PowerWatts           float64
	TemperatureCelsius   float64
	PowerAvailable       bool
	TemperatureAvailable bool
	PowerError           string
	TemperatureError     string
}

type energyCounter struct {
	path  string
	start int64
	max   int64
}

func ReadCPU(ctx context.Context) CPUSnapshot {
	return readCPU(ctx, "/sys", cpuPowerSampleInterval, waitForContext, time.Now)
}

func readCPU(
	ctx context.Context,
	sysRoot string,
	sampleInterval time.Duration,
	wait func(context.Context, time.Duration) error,
	now func() time.Time,
) CPUSnapshot {
	snapshot := CPUSnapshot{}
	power, err := readPackagePower(ctx, sysRoot, sampleInterval, wait, now)
	if err != nil {
		snapshot.PowerError = err.Error()
	} else {
		snapshot.PowerWatts = power
		snapshot.PowerAvailable = true
	}

	temperature, err := readPackageTemperature(sysRoot)
	if err != nil {
		snapshot.TemperatureError = err.Error()
	} else {
		snapshot.TemperatureCelsius = temperature
		snapshot.TemperatureAvailable = true
	}
	return snapshot
}

func readPackagePower(
	ctx context.Context,
	sysRoot string,
	sampleInterval time.Duration,
	wait func(context.Context, time.Duration) error,
	now func() time.Time,
) (float64, error) {
	domains, err := filepath.Glob(filepath.Join(sysRoot, "class/powercap/intel-rapl:*"))
	if err != nil {
		return 0, fmt.Errorf("find RAPL domains: %w", err)
	}
	sort.Strings(domains)
	counters := make([]energyCounter, 0, len(domains))
	for _, domain := range domains {
		name, readErr := readText(filepath.Join(domain, "name"))
		if readErr != nil || !strings.HasPrefix(name, "package-") {
			continue
		}
		start, readErr := readInt64(filepath.Join(domain, "energy_uj"))
		if readErr != nil {
			return 0, fmt.Errorf("read %s RAPL energy: %w", name, readErr)
		}
		maxEnergy, readErr := readInt64(filepath.Join(domain, "max_energy_range_uj"))
		if readErr != nil {
			return 0, fmt.Errorf("read %s RAPL range: %w", name, readErr)
		}
		counters = append(counters, energyCounter{path: filepath.Join(domain, "energy_uj"), start: start, max: maxEnergy})
	}
	if len(counters) == 0 {
		return 0, errors.New("CPU package RAPL domain not found")
	}

	startedAt := now()
	if err := wait(ctx, sampleInterval); err != nil {
		return 0, err
	}
	elapsed := now().Sub(startedAt).Seconds()
	if elapsed <= 0 {
		return 0, errors.New("RAPL sample duration is not positive")
	}

	var deltaMicrojoules int64
	for _, counter := range counters {
		end, readErr := readInt64(counter.path)
		if readErr != nil {
			return 0, fmt.Errorf("read RAPL energy after sampling: %w", readErr)
		}
		delta := end - counter.start
		if delta < 0 {
			delta = counter.max - counter.start + end
		}
		if delta < 0 {
			return 0, errors.New("invalid RAPL energy delta")
		}
		deltaMicrojoules += delta
	}
	return float64(deltaMicrojoules) / 1_000_000 / elapsed, nil
}

func readPackageTemperature(sysRoot string) (float64, error) {
	devices, err := filepath.Glob(filepath.Join(sysRoot, "class/hwmon/hwmon*"))
	if err != nil {
		return 0, fmt.Errorf("find hwmon devices: %w", err)
	}
	var hottest float64
	found := false
	for _, device := range devices {
		name, readErr := readText(filepath.Join(device, "name"))
		if readErr != nil || name != "coretemp" {
			continue
		}
		labels, globErr := filepath.Glob(filepath.Join(device, "temp*_label"))
		if globErr != nil {
			return 0, fmt.Errorf("find package temperature labels: %w", globErr)
		}
		for _, labelPath := range labels {
			label, readErr := readText(labelPath)
			if readErr != nil || !strings.HasPrefix(label, "Package id ") {
				continue
			}
			inputPath := strings.TrimSuffix(labelPath, "_label") + "_input"
			milliCelsius, readErr := readInt64(inputPath)
			if readErr != nil {
				return 0, fmt.Errorf("read CPU package temperature: %w", readErr)
			}
			celsius := float64(milliCelsius) / 1000
			if !found || celsius > hottest {
				hottest = celsius
				found = true
			}
		}
	}
	if !found {
		return 0, errors.New("CPU package temperature sensor not found")
	}
	return hottest, nil
}

func waitForContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func readText(path string) (string, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(value)), nil
}

func readInt64(path string) (int64, error) {
	value, err := readText(path)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	return parsed, nil
}
