// Package metrics samples host CPU, memory, load and network from /proc (Linux).
package metrics

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"

	nodev1 "github.com/vyto4ka/vynnel/internal/proto/vynnel/node/v1"
)

// Collector keeps the previous sample to compute rates.
type Collector struct {
	prevCPUIdle, prevCPUTotal uint64
	prevRx, prevTx            uint64
	prevAt                    time.Time
}

// Sample returns current metrics; rates are zero on the first call.
func (c *Collector) Sample() *nodev1.Metrics {
	m := &nodev1.Metrics{}
	now := time.Now()
	if idle, total, ok := cpuTimes(); ok {
		if c.prevCPUTotal > 0 && total > c.prevCPUTotal {
			dt := float64(total - c.prevCPUTotal)
			m.Cpu = 100 * (1 - float64(idle-c.prevCPUIdle)/dt)
		}
		c.prevCPUIdle, c.prevCPUTotal = idle, total
	}
	m.MemTotal, m.MemUsed = memory()
	m.Load1 = load1()
	m.Uptime = uptime()
	if rx, tx, ok := netBytes(); ok {
		if !c.prevAt.IsZero() && rx >= c.prevRx && tx >= c.prevTx {
			sec := now.Sub(c.prevAt).Seconds()
			if sec > 0 {
				m.RxBps = uint64(float64(rx-c.prevRx) / sec)
				m.TxBps = uint64(float64(tx-c.prevTx) / sec)
			}
		}
		c.prevRx, c.prevTx = rx, tx
	}
	c.prevAt = now
	return m
}

func cpuTimes() (idle, total uint64, ok bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0, false
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	for i, v := range fields[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
		if i == 3 || i == 4 { // idle, iowait
			idle += n
		}
	}
	return idle, total, true
}

func memory() (total, used uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	vals := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 {
			n, _ := strconv.ParseUint(fields[1], 10, 64)
			vals[strings.TrimSuffix(fields[0], ":")] = n * 1024
		}
	}
	total = vals["MemTotal"]
	if avail, ok := vals["MemAvailable"]; ok && avail <= total {
		used = total - avail
	}
	return total, used
}

func load1() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

func uptime() uint64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return uint64(v)
}

// netBytes sums received/transmitted bytes over non-loopback interfaces.
func netBytes() (rx, tx uint64, ok bool) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, rest, found := strings.Cut(sc.Text(), ":")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "lo" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(fields[0], 10, 64)
		t, _ := strconv.ParseUint(fields[8], 10, 64)
		rx += r
		tx += t
	}
	return rx, tx, true
}
