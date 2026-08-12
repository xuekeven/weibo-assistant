package systemmetrics

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadCPUCalculatesPowerAndPackageTemperature(t *testing.T) {
	root := t.TempDir()
	powerDir := filepath.Join(root, "class/powercap/intel-rapl:0")
	hwmonDir := filepath.Join(root, "class/hwmon/hwmon0")
	if err := os.MkdirAll(powerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(hwmonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(powerDir, "name"), "package-0\n")
	writeTestFile(t, filepath.Join(powerDir, "energy_uj"), "10000000\n")
	writeTestFile(t, filepath.Join(powerDir, "max_energy_range_uj"), "100000000\n")
	writeTestFile(t, filepath.Join(hwmonDir, "name"), "coretemp\n")
	writeTestFile(t, filepath.Join(hwmonDir, "temp1_label"), "Package id 0\n")
	writeTestFile(t, filepath.Join(hwmonDir, "temp1_input"), "51500\n")

	currentTime := time.Unix(100, 0)
	wait := func(_ context.Context, duration time.Duration) error {
		writeTestFile(t, filepath.Join(powerDir, "energy_uj"), "30000000\n")
		currentTime = currentTime.Add(duration)
		return nil
	}
	snapshot := readCPU(context.Background(), root, 2*time.Second, wait, func() time.Time { return currentTime })
	if !snapshot.PowerAvailable || snapshot.PowerWatts != 10 {
		t.Fatalf("unexpected power snapshot: %#v", snapshot)
	}
	if !snapshot.TemperatureAvailable || snapshot.TemperatureCelsius != 51.5 {
		t.Fatalf("unexpected temperature snapshot: %#v", snapshot)
	}
}

func TestReadPackagePowerHandlesCounterWrap(t *testing.T) {
	root := t.TempDir()
	powerDir := filepath.Join(root, "class/powercap/intel-rapl:0")
	if err := os.MkdirAll(powerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(powerDir, "name"), "package-0\n")
	writeTestFile(t, filepath.Join(powerDir, "energy_uj"), "95000000\n")
	writeTestFile(t, filepath.Join(powerDir, "max_energy_range_uj"), "100000000\n")

	currentTime := time.Unix(100, 0)
	wait := func(_ context.Context, duration time.Duration) error {
		writeTestFile(t, filepath.Join(powerDir, "energy_uj"), "5000000\n")
		currentTime = currentTime.Add(duration)
		return nil
	}
	power, err := readPackagePower(context.Background(), root, 2*time.Second, wait, func() time.Time { return currentTime })
	if err != nil {
		t.Fatalf("readPackagePower() error = %v", err)
	}
	if power != 5 {
		t.Fatalf("power = %v, want 5", power)
	}
}

func writeTestFile(t *testing.T, path string, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}
