// Package metrics samples host CPU, memory, load and network from /proc (Linux).
package metrics

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"

	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
)

// Collector keeps the previous sample to compute rates.
type Collector struct {
	prevCPU        cpuStat
	prevRx, prevTx uint64
	prevAt         time.Time
}

// Sample returns current metrics; rates are zero on the first call.
func (c *Collector) Sample() *nodev1.Metrics {
	m := &nodev1.Metrics{}
	now := time.Now()
	if cs, ok := cpuTimes(); ok {
		if c.prevCPU.total > 0 && cs.total > c.prevCPU.total {
			dt := float64(cs.total - c.prevCPU.total)
			m.Cpu = 100 * (1 - float64(cs.idle-c.prevCPU.idle)/dt)
			if cs.steal >= c.prevCPU.steal {
				m.Steal = 100 * float64(cs.steal-c.prevCPU.steal) / dt
			}
		}
		c.prevCPU = cs
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

// cpuStat is the aggregate line of /proc/stat, in ticks.
type cpuStat struct{ idle, steal, total uint64 }

func cpuTimes() (cpuStat, bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuStat{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return cpuStat{}, false
	}
	return parseCPU(sc.Text())
}

// parseCPU reads "cpu user nice system idle iowait irq softirq steal guest guest_nice". Steal
// is time a busy hypervisor gave to other guests: it counts as busy, and is reported apart too.
// guest and guest_nice are already inside user and nice.
func parseCPU(line string) (cpuStat, bool) {
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuStat{}, false
	}
	var cs cpuStat
	for i, v := range fields[1:] {
		if i >= 8 { // guest, guest_nice
			break
		}
		n, _ := strconv.ParseUint(v, 10, 64)
		cs.total += n
		switch i {
		case 3, 4: // idle, iowait
			cs.idle += n
		case 7:
			cs.steal = n
		}
	}
	return cs, true
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
