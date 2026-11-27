package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxTemp: new tasks wait while the CPU package is this hot (90°C; the PC
// once froze under a 24-core load).
var maxTemp = 90

var thermalMu sync.Mutex

// thermalWait blocks while the CPU package is at or above maxTemp, until it
// has cooled by 8°C. A no-op when off or when the temperature is unreadable.
func thermalWait() {
	if maxTemp <= 0 {
		return
	}
	if t, ok := packageTemp(); !ok || t < maxTemp {
		return
	}
	thermalMu.Lock()
	defer thermalMu.Unlock()
	t, ok := packageTemp()
	if !ok || t < maxTemp {
		return
	}
	fmt.Fprintf(os.Stderr, "\nCPU at %d°C: pausing new tasks until %d°C\n", t, maxTemp-8)
	for {
		time.Sleep(20 * time.Second)
		if t, ok = packageTemp(); !ok || t <= maxTemp-8 {
			return
		}
	}
}

// packageTemp reads the coretemp package temperature (Linux hwmon).
func packageTemp() (int, bool) {
	dirs, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	best := -1
	for _, d := range dirs {
		name, err := os.ReadFile(filepath.Join(d, "name"))
		if err != nil || strings.TrimSpace(string(name)) != "coretemp" {
			continue
		}
		inputs, _ := filepath.Glob(filepath.Join(d, "temp*_input"))
		for _, in := range inputs {
			raw, err := os.ReadFile(in)
			if err != nil {
				continue
			}
			milli, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil {
				continue
			}
			label, _ := os.ReadFile(strings.TrimSuffix(in, "_input") + "_label")
			if strings.HasPrefix(strings.TrimSpace(string(label)), "Package") {
				return milli / 1000, true
			}
			best = max(best, milli/1000)
		}
	}
	return best, best >= 0
}
