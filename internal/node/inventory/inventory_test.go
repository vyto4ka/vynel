package inventory

import (
	"context"
	"testing"
)

func TestParsers(t *testing.T) {
	pretty, id, ver := parseOSRelease("NAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nID=ubuntu\nPRETTY_NAME=\"Ubuntu 24.04.2 LTS\"\n")
	if pretty != "Ubuntu 24.04.2 LTS" || id != "ubuntu" || ver != "24.04" {
		t.Fatalf("parseOSRelease = (%q, %q, %q)", pretty, id, ver)
	}

	var hw HardwareInfo
	parseCPUInfo(`processor	: 0
model name	: AMD EPYC 9654 96-Core Processor
cpu MHz		: 2400.000
cache size	: 1024 KB
physical id	: 0
core id		: 0
flags		: fpu sse4_2 aes avx2

processor	: 1
model name	: AMD EPYC 9654 96-Core Processor
physical id	: 0
core id		: 1
flags		: fpu sse4_2 aes avx2
`, &hw)
	if hw.CPUModel != "AMD EPYC 9654 96-Core Processor" || hw.CPUPhysical != 2 || !hw.AESHardware || hw.CPUCache != "1024 KB" {
		t.Fatalf("parseCPUInfo = %+v", hw)
	}

	parseMemInfo("MemTotal:        4096000 kB\nMemAvailable:    3072000 kB\nSwapTotal:       1024000 kB\nSwapFree:         512000 kB\n", &hw)
	if hw.MemTotal != 4096000*1024 || hw.MemUsed != 1024000*1024 || hw.SwapTotal != 1024000*1024 || hw.SwapUsed != 512000*1024 {
		t.Fatalf("parseMemInfo = %+v", hw)
	}

	devs := parseNetDev(`Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
  eth0: 123456 789 1 2 0 0 0 0 654321 987 3 4 0 0 0 0
`)
	if d := devs["eth0"]; d.rxBytes != 123456 || d.txBytes != 654321 || d.rxErrors != 1 || d.rxDrops != 2 || d.txErrors != 3 || d.txDrops != 4 {
		t.Fatalf("parseNetDev = %+v", d)
	}

	gws := parseDefaultGatewaysV4("Iface\tDestination\tGateway\tFlags\nens3\t00000000\t0100A8C0\t0003\n")
	if gws["ens3"] != "192.168.0.1" {
		t.Fatalf("parseDefaultGatewaysV4 = %v", gws)
	}

	for in, want := range map[string]string{"vda1": "vda", "sda12": "sda", "nvme0n1p1": "nvme0n1", "mmcblk0p2": "mmcblk0"} {
		if got := baseBlockName(in); got != want {
			t.Errorf("baseBlockName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCollectSmoke(t *testing.T) {
	snap := Collect(context.Background(), t.TempDir())
	if snap.CollectedAt <= 0 || snap.OS.Arch == "" || snap.Hardware.CPUCores < 1 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
}
