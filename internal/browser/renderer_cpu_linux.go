//go:build linux

package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func readRendererProcessSamples(rootPID int) ([]rendererProcessSample, error) {
	queue := []int{rootPID}
	seen := map[int]bool{}
	var result []rendererProcessSample
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		children, err := readProcessChildren(pid)
		if err == nil {
			queue = append(queue, children...)
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
		if err != nil || !isRendererCommandLine(cmdline) {
			continue
		}
		ticks, err := readProcessCPUTicks(pid)
		if err == nil {
			result = append(result, rendererProcessSample{pid: pid, ticks: ticks})
		}
	}
	return result, nil
}

func readProcessChildren(pid int) ([]int, error) {
	value, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "task", strconv.Itoa(pid), "children"))
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(value))
	children := make([]int, 0, len(fields))
	for _, field := range fields {
		child, parseErr := strconv.Atoi(field)
		if parseErr == nil {
			children = append(children, child)
		}
	}
	return children, nil
}

func readProcessCPUTicks(pid int) (uint64, error) {
	value, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, err
	}
	// comm is parenthesized and may contain spaces; fields after the final ')' start at field 3.
	closing := strings.LastIndexByte(string(value), ')')
	if closing < 0 {
		return 0, fmt.Errorf("invalid /proc/%d/stat", pid)
	}
	fields := strings.Fields(string(value[closing+1:]))
	if len(fields) <= 12 {
		return 0, fmt.Errorf("short /proc/%d/stat", pid)
	}
	userTicks, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return 0, err
	}
	systemTicks, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return 0, err
	}
	return userTicks + systemTicks, nil
}
