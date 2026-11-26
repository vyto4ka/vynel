// Package inventory collects OS, hardware, disk and network interface facts of a node host.
package inventory

import (
	"bufio"
	"cmp"
	"context"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/node/sysctl"
)

// minValidInstallUnix filters out epoch/placeholder timestamps (2000-01-01 UTC).
const minValidInstallUnix = 946684800

// Snapshot is a point-in-time inventory of a node's OS, hardware, storage and network interfaces.
type Snapshot struct {
	CollectedAt int64          `json:"collectedAt"`
	OS          OSInfo         `json:"os"`
	Hardware    HardwareInfo   `json:"hardware"`
	Disks       []DiskInfo     `json:"disks"`
	Interfaces  []NetInterface `json:"interfaces"`
}

// OSInfo describes the operating system, kernel, virtualization and OS age.
type OSInfo struct {
	Distro            string `json:"distro"`
	ID                string `json:"id,omitempty"`
	VersionID         string `json:"versionId,omitempty"`
	Kernel            string `json:"kernel"`
	Arch              string `json:"arch"`
	Hostname          string `json:"hostname,omitempty"`
	Virtualization    string `json:"virtualization,omitempty"`
	InstalledAt       int64  `json:"installedAt,omitempty"`
	InstalledSource   string `json:"installedSource,omitempty"`
	BootedAt          int64  `json:"bootedAt,omitempty"`
	Uptime            uint64 `json:"uptime"`
	Timezone          string `json:"timezone,omitempty"`
	NTPSync           *bool  `json:"ntpSync,omitempty"`
	CongestionControl string `json:"congestionControl,omitempty"`
	Qdisc             string `json:"qdisc,omitempty"`
	TCPFastopen       string `json:"tcpFastopen,omitempty"`
}

// HardwareInfo describes the CPU, DMI platform, memory/swap and load averages.
type HardwareInfo struct {
	CPUModel     string  `json:"cpuModel"`
	CPUCores     int     `json:"cpuCores"`
	CPUPhysical  int     `json:"cpuPhysical,omitempty"`
	CPUMHz       float64 `json:"cpuMhz,omitempty"`
	CPUCache     string  `json:"cpuCache,omitempty"`
	AESHardware  bool    `json:"aesHardware"`
	Vendor       string  `json:"vendor,omitempty"`
	Product      string  `json:"product,omitempty"`
	MemTotal     uint64  `json:"memTotal"`
	MemUsed      uint64  `json:"memUsed"`
	MemAvailable uint64  `json:"memAvailable"`
	SwapTotal    uint64  `json:"swapTotal"`
	SwapUsed     uint64  `json:"swapUsed"`
	Load1        float64 `json:"load1"`
	Load5        float64 `json:"load5"`
	Load15       float64 `json:"load15"`
}

// DiskInfo describes a mounted filesystem (root "/" and optional separate dataDir mount).
type DiskInfo struct {
	Mount      string `json:"mount"`
	Device     string `json:"device,omitempty"`
	FSType     string `json:"fsType,omitempty"`
	Kind       string `json:"kind,omitempty"` // "NVMe" | "SSD" | "HDD"
	TotalBytes uint64 `json:"totalBytes"`
	UsedBytes  uint64 `json:"usedBytes"`
	FreeBytes  uint64 `json:"freeBytes"`
}

// NetInterface describes one host network interface with its addresses and cumulative counters.
type NetInterface struct {
	Name           string   `json:"name"`
	Kind           string   `json:"kind"` // "physical" | "virtual" | "loopback"
	Up             bool     `json:"up"`
	OperState      string   `json:"operState,omitempty"`
	MAC            string   `json:"mac,omitempty"`
	MTU            int      `json:"mtu"`
	SpeedMbps      int      `json:"speedMbps,omitempty"`
	Primary        bool     `json:"primary"`
	DefaultGateway string   `json:"defaultGateway,omitempty"`
	Addresses      []string `json:"addresses"`
	RxBytes        uint64   `json:"rxBytes"`
	TxBytes        uint64   `json:"txBytes"`
	RxPackets      uint64   `json:"rxPackets"`
	TxPackets      uint64   `json:"txPackets"`
	RxErrors       uint64   `json:"rxErrors"`
	TxErrors       uint64   `json:"txErrors"`
	RxDrops        uint64   `json:"rxDrops"`
	TxDrops        uint64   `json:"txDrops"`
}

// Collect gathers a full host inventory snapshot. All reads are local and bounded by ctx.
func Collect(ctx context.Context, dataDir string) Snapshot {
	now := time.Now()
	s := Snapshot{
		CollectedAt: now.Unix(),
		OS:          collectOS(ctx, now),
		Hardware:    collectHardware(),
		Disks:       collectDisks(dataDir),
		Interfaces:  collectInterfaces(),
	}
	return s
}

func collectOS(ctx context.Context, now time.Time) OSInfo {
	info := OSInfo{
		Distro:            runtime.GOOS,
		Arch:              runtime.GOARCH,
		Kernel:            readTrimmed("/proc/sys/kernel/osrelease"),
		CongestionControl: sysctl.Read("net.ipv4.tcp_congestion_control"),
		Qdisc:             sysctl.Read("net.core.default_qdisc"),
		TCPFastopen:       sysctl.Read("net.ipv4.tcp_fastopen"),
	}
	if h, err := os.Hostname(); err == nil {
		info.Hostname = strings.TrimSpace(h)
	}
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		pretty, id, ver := parseOSRelease(string(b))
		if pretty != "" {
			info.Distro = pretty
		}
		info.ID = id
		info.VersionID = ver
	}
	info.Uptime = readUptime()
	if info.Uptime > 0 {
		info.BootedAt = now.Unix() - int64(info.Uptime)
	}
	info.InstalledAt, info.InstalledSource = detectOSInstalledAt(ctx, now)
	info.Virtualization = detectVirtualization(info.Kernel)
	info.Timezone = detectTimezone(now)
	info.NTPSync = detectNTPSync(ctx)
	return info
}

func parseOSRelease(raw string) (pretty, id, versionID string) {
	vals := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		vals[strings.TrimSpace(k)] = v
	}
	pretty = vals["PRETTY_NAME"]
	if pretty == "" && vals["NAME"] != "" {
		pretty = strings.TrimSpace(vals["NAME"] + " " + vals["VERSION_ID"])
	}
	return pretty, vals["ID"], vals["VERSION_ID"]
}

func detectOSInstalledAt(ctx context.Context, now time.Time) (int64, string) {
	maxUnix := now.Unix() + 60
	valid := func(ts int64) bool { return ts >= minValidInstallUnix && ts <= maxUnix }

	// 1. Birth time of "/" via stat -c %W (ext4/xfs/btrfs/zfs creation timestamp, non-root).
	if ts := statBirthUnix(ctx, "/"); valid(ts) {
		return ts, "rootfs"
	}
	// 2. Fallback files created during initial OS provisioning.
	candidates := []struct {
		path   string
		source string
	}{
		{"/var/log/installer", "installer"},
		{"/etc/machine-id", "machine-id"},
		{"/lost+found", "lost+found"},
		{"/etc/ssh/ssh_host_ed25519_key.pub", "ssh-host-key"},
	}
	for _, c := range candidates {
		if ts := statBirthUnix(ctx, c.path); valid(ts) {
			return ts, c.source
		}
		if fi, err := os.Stat(c.path); err == nil {
			if ts := fi.ModTime().Unix(); valid(ts) {
				return ts, c.source
			}
		}
	}
	return 0, ""
}

func statBirthUnix(ctx context.Context, path string) int64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	cctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(cctx, "stat", "-c", "%W", path).Output()
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func detectVirtualization(kernel string) string {
	kl := strings.ToLower(kernel)
	if strings.Contains(kl, "microsoft") || strings.Contains(kl, "wsl") {
		return "WSL2"
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "Docker"
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return "Podman"
	}
	if env, err := os.ReadFile("/proc/1/environ"); err == nil {
		s := string(env)
		switch {
		case strings.Contains(s, "container=lxc"):
			return "LXC"
		case strings.Contains(s, "container=systemd-nspawn"):
			return "systemd-nspawn"
		case strings.Contains(s, "container=podman"):
			return "Podman"
		case strings.Contains(s, "container=docker"):
			return "Docker"
		}
	}
	if cg, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		s := strings.ToLower(string(cg))
		switch {
		case strings.Contains(s, "kubepods"):
			return "Kubernetes"
		case strings.Contains(s, "docker"):
			return "Docker"
		case strings.Contains(s, "lxc"):
			return "LXC"
		}
	}
	if _, err := os.Stat("/proc/vz"); err == nil {
		if _, err2 := os.Stat("/proc/bc"); err2 != nil {
			return "OpenVZ"
		}
	}
	vendor := readTrimmed("/sys/class/dmi/id/sys_vendor")
	product := readTrimmed("/sys/class/dmi/id/product_name")
	combined := strings.ToLower(vendor + " " + product)
	switch {
	case strings.Contains(combined, "cloud hypervisor") || strings.Contains(combined, "cloud-hypervisor"):
		return "Cloud Hypervisor"
	case strings.Contains(combined, "hetzner"):
		return "KVM (Hetzner)"
	case strings.Contains(combined, "digitalocean"):
		return "KVM (DigitalOcean)"
	case strings.Contains(combined, "google compute engine"):
		return "KVM (Google Cloud)"
	case strings.Contains(combined, "amazon ec2"):
		return "Nitro / KVM (AWS)"
	case strings.Contains(combined, "qemu") || strings.Contains(combined, "kvm") || strings.Contains(combined, "bochs"):
		return "KVM / QEMU"
	case strings.Contains(combined, "vmware"):
		return "VMware"
	case strings.Contains(combined, "virtual machine") && strings.Contains(combined, "microsoft"):
		return "Hyper-V"
	case strings.Contains(combined, "virtualbox"):
		return "VirtualBox"
	case strings.Contains(combined, "xen"):
		return "Xen"
	}
	if hyp := readTrimmed("/sys/hypervisor/type"); hyp != "" {
		return hyp
	}
	if vendor != "" || product != "" {
		return "Bare Metal"
	}
	return ""
}

func detectTimezone(now time.Time) string {
	if tz := readTrimmed("/etc/timezone"); tz != "" {
		return tz
	}
	if link, err := os.Readlink("/etc/localtime"); err == nil {
		if _, after, ok := strings.Cut(link, "/zoneinfo/"); ok && after != "" {
			return after
		}
	}
	name, _ := now.Zone()
	return name
}

func detectNTPSync(ctx context.Context) *bool {
	if runtime.GOOS != "linux" {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(cctx, "timedatectl", "show", "-p", "NTPSynchronized", "--value").Output()
	if err != nil {
		return nil
	}
	switch strings.TrimSpace(string(out)) {
	case "yes":
		v := true
		return &v
	case "no":
		v := false
		return &v
	}
	return nil
}

func collectHardware() HardwareInfo {
	hw := HardwareInfo{
		CPUModel: runtime.GOARCH,
		CPUCores: runtime.NumCPU(),
		Vendor:   cleanDMI(readTrimmed("/sys/class/dmi/id/sys_vendor")),
		Product:  cleanDMI(readTrimmed("/sys/class/dmi/id/product_name")),
	}
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		parseCPUInfo(string(b), &hw)
	}
	if hw.CPUMHz == 0 {
		if khz, err := strconv.ParseFloat(readTrimmed("/sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq"), 64); err == nil && khz > 0 {
			hw.CPUMHz = khz / 1000
		}
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		parseMemInfo(string(b), &hw)
	}
	hw.Load1, hw.Load5, hw.Load15 = readLoadAvg()
	return hw
}

func cleanDMI(s string) string {
	l := strings.ToLower(s)
	if s == "" || l == "default string" || l == "to be filled by o.e.m." || l == "system product name" || l == "system manufacturer" {
		return ""
	}
	return s
}

func parseCPUInfo(raw string, hw *HardwareInfo) {
	var model, hardwareField, procField string
	physPairs := map[string]bool{}
	var curPhys, curCore string
	var cpuCoresField int

	flushCore := func() {
		if curPhys != "" && curCore != "" {
			physPairs[curPhys+":"+curCore] = true
		}
		curPhys, curCore = "", ""
	}

	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			flushCore()
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(strings.ToLower(k))
		v = strings.TrimSpace(v)
		switch k {
		case "model name":
			if model == "" {
				model = v
			}
		case "hardware", "model":
			if hardwareField == "" {
				hardwareField = v
			}
		case "processor":
			if procField == "" && !isDigits(v) {
				procField = v
			}
		case "cpu mhz":
			if hw.CPUMHz == 0 {
				hw.CPUMHz, _ = strconv.ParseFloat(v, 64)
			}
		case "cache size":
			if hw.CPUCache == "" {
				hw.CPUCache = v
			}
		case "physical id":
			curPhys = v
		case "core id":
			curCore = v
		case "cpu cores":
			if cpuCoresField == 0 {
				cpuCoresField, _ = strconv.Atoi(v)
			}
		case "flags", "features":
			if !hw.AESHardware {
				for _, f := range strings.Fields(strings.ToLower(v)) {
					if f == "aes" || f == "pmull" {
						hw.AESHardware = true
						break
					}
				}
			}
		}
	}
	flushCore()

	switch {
	case model != "":
		hw.CPUModel = model
	case hardwareField != "":
		hw.CPUModel = hardwareField
	case procField != "":
		hw.CPUModel = procField
	}
	if len(physPairs) > 0 {
		hw.CPUPhysical = len(physPairs)
	} else if cpuCoresField > 0 {
		hw.CPUPhysical = cpuCoresField
	}
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func parseMemInfo(raw string, hw *HardwareInfo) {
	vals := map[string]uint64{}
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 {
			n, _ := strconv.ParseUint(fields[1], 10, 64)
			vals[strings.TrimSuffix(fields[0], ":")] = n * 1024
		}
	}
	hw.MemTotal = vals["MemTotal"]
	if avail, ok := vals["MemAvailable"]; ok && avail <= hw.MemTotal {
		hw.MemAvailable = avail
		hw.MemUsed = hw.MemTotal - avail
	}
	hw.SwapTotal = vals["SwapTotal"]
	if sf, ok := vals["SwapFree"]; ok && sf <= hw.SwapTotal {
		hw.SwapUsed = hw.SwapTotal - sf
	}
}

func readLoadAvg() (l1, l5, l15 float64) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0
	}
	f := strings.Fields(string(b))
	if len(f) >= 3 {
		l1, _ = strconv.ParseFloat(f[0], 64)
		l5, _ = strconv.ParseFloat(f[1], 64)
		l15, _ = strconv.ParseFloat(f[2], 64)
	}
	return l1, l5, l15
}

func readUptime() uint64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	if v < 0 {
		return 0
	}
	return uint64(v)
}

type mountEntry struct {
	device string
	mount  string
	fsType string
}

func parseMounts(raw string) []mountEntry {
	var out []mountEntry
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 3 {
			out = append(out, mountEntry{
				device: unescapeOctal(f[0]),
				mount:  unescapeOctal(f[1]),
				fsType: f[2],
			})
		}
	}
	return out
}

func unescapeOctal(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}

func findMountForPath(mounts []mountEntry, target string) (mountEntry, bool) {
	clean := filepath.Clean(target)
	var best mountEntry
	bestLen := -1
	for _, m := range mounts {
		mc := filepath.Clean(m.mount)
		if mc == "/" || clean == mc || strings.HasPrefix(clean, mc+"/") {
			if len(mc) >= bestLen {
				// Later mounts in /proc/mounts shadow earlier ones on the same mountpoint.
				best, bestLen = m, len(mc)
			}
		}
	}
	return best, bestLen >= 0
}

func collectDisks(dataDir string) []DiskInfo {
	var mounts []mountEntry
	if b, err := os.ReadFile("/proc/mounts"); err == nil {
		mounts = parseMounts(string(b))
	}
	targets := []string{"/"}
	if dataDir != "" {
		targets = append(targets, dataDir)
	}
	seenMounts := map[string]bool{}
	seenFSID := map[string]bool{}
	var out []DiskInfo
	for _, p := range targets {
		m, ok := findMountForPath(mounts, p)
		mountPath := "/"
		if ok {
			mountPath = m.mount
		} else if p != "/" {
			mountPath = p
		}
		if seenMounts[mountPath] {
			continue
		}
		total, used, free, fsid, statOK := statFilesystem(p)
		if !statOK || total == 0 {
			continue
		}
		if fsid != "" && seenFSID[fsid] {
			continue
		}
		seenMounts[mountPath] = true
		if fsid != "" {
			seenFSID[fsid] = true
		}
		out = append(out, DiskInfo{
			Mount:      mountPath,
			Device:     m.device,
			FSType:     m.fsType,
			Kind:       detectDiskKind(m.device),
			TotalBytes: total,
			UsedBytes:  used,
			FreeBytes:  free,
		})
	}
	return out
}

func detectDiskKind(device string) string {
	if !strings.HasPrefix(device, "/dev/") {
		return ""
	}
	name := filepath.Base(device)
	if strings.HasPrefix(name, "nvme") {
		return "NVMe"
	}
	base := baseBlockName(name)
	if base == "" {
		return ""
	}
	switch readTrimmed(filepath.Join("/sys/block", base, "queue", "rotational")) {
	case "0":
		return "SSD"
	case "1":
		return "HDD"
	}
	return ""
}

func baseBlockName(name string) string {
	// Strip partition suffix: vda1 -> vda, sda2 -> sda, mmcblk0p1 -> mmcblk0, nvme0n1p1 -> nvme0n1.
	if (strings.HasPrefix(name, "nvme") || strings.HasPrefix(name, "mmcblk") || strings.HasPrefix(name, "loop")) && strings.Contains(name, "p") {
		if i := strings.LastIndexByte(name, 'p'); i > 0 && isDigits(name[i+1:]) {
			return name[:i]
		}
	}
	i := len(name)
	for i > 0 && name[i-1] >= '0' && name[i-1] <= '9' {
		i--
	}
	if i > 0 {
		return name[:i]
	}
	return name
}

type netDevStats struct {
	rxBytes, rxPackets, rxErrors, rxDrops uint64
	txBytes, txPackets, txErrors, txDrops uint64
}

func parseNetDev(raw string) map[string]netDevStats {
	out := map[string]netDevStats{}
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		f := strings.Fields(rest)
		if len(f) < 12 {
			continue
		}
		var st netDevStats
		st.rxBytes, _ = strconv.ParseUint(f[0], 10, 64)
		st.rxPackets, _ = strconv.ParseUint(f[1], 10, 64)
		st.rxErrors, _ = strconv.ParseUint(f[2], 10, 64)
		st.rxDrops, _ = strconv.ParseUint(f[3], 10, 64)
		st.txBytes, _ = strconv.ParseUint(f[8], 10, 64)
		st.txPackets, _ = strconv.ParseUint(f[9], 10, 64)
		st.txErrors, _ = strconv.ParseUint(f[10], 10, 64)
		st.txDrops, _ = strconv.ParseUint(f[11], 10, 64)
		out[name] = st
	}
	return out
}

func parseDefaultGatewaysV4(raw string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[0] == "Iface" || f[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 16)
		if err != nil || flags&0x2 == 0 { // RTF_GATEWAY = 0x2
			continue
		}
		b, err := hex.DecodeString(f[2])
		if err != nil || len(b) != 4 {
			continue
		}
		// /proc/net/route stores IPv4 in host byte order (little-endian on amd64/arm64).
		ip := net.IPv4(b[3], b[2], b[1], b[0]).String()
		if _, exists := out[f[0]]; !exists {
			out[f[0]] = ip
		}
	}
	return out
}

var virtualNamePrefixes = []string{"docker", "br-", "veth", "virbr", "cni", "flannel", "cali", "lxc", "tun", "tap", "wg", "sit", "ip6tnl", "gre", "tailscale", "zt"}

func classifyInterface(ifc net.Interface) string {
	if ifc.Flags&net.FlagLoopback != 0 || ifc.Name == "lo" {
		return "loopback"
	}
	if link, err := os.Readlink(filepath.Join("/sys/class/net", ifc.Name)); err == nil {
		if strings.Contains(link, "/devices/virtual/") {
			return "virtual"
		}
		return "physical"
	}
	for _, p := range virtualNamePrefixes {
		if strings.HasPrefix(ifc.Name, p) {
			return "virtual"
		}
	}
	return "physical"
}

func collectInterfaces() []NetInterface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	stats := map[string]netDevStats{}
	if b, err := os.ReadFile("/proc/net/dev"); err == nil {
		stats = parseNetDev(string(b))
	}
	gateways := map[string]string{}
	if b, err := os.ReadFile("/proc/net/route"); err == nil {
		gateways = parseDefaultGatewaysV4(string(b))
	}
	prim4 := primaryRouteIP("udp4", "1.1.1.1:53")
	prim6 := primaryRouteIP("udp6", "[2606:4700:4700::1111]:53")

	out := make([]NetInterface, 0, len(ifaces))
	for _, ifc := range ifaces {
		kind := classifyInterface(ifc)
		oper := strings.ToLower(readTrimmed(filepath.Join("/sys/class/net", ifc.Name, "operstate")))
		up := ifc.Flags&net.FlagUp != 0 && oper != "down"
		var speed int
		if sp, err := strconv.Atoi(readTrimmed(filepath.Join("/sys/class/net", ifc.Name, "speed"))); err == nil && sp > 0 {
			speed = sp
		}
		var addrs []string
		isPrimary := false
		if rawAddrs, err := ifc.Addrs(); err == nil {
			for _, a := range rawAddrs {
				addrs = append(addrs, a.String())
				if ipnet, ok := a.(*net.IPNet); ok {
					if (prim4 != nil && ipnet.IP.Equal(prim4)) || (prim6 != nil && ipnet.IP.Equal(prim6)) {
						isPrimary = true
					}
				}
			}
		}
		if addrs == nil {
			addrs = []string{}
		}
		st := stats[ifc.Name]
		out = append(out, NetInterface{
			Name:           ifc.Name,
			Kind:           kind,
			Up:             up,
			OperState:      oper,
			MAC:            ifc.HardwareAddr.String(),
			MTU:            ifc.MTU,
			SpeedMbps:      speed,
			Primary:        isPrimary,
			DefaultGateway: gateways[ifc.Name],
			Addresses:      addrs,
			RxBytes:        st.rxBytes,
			TxBytes:        st.txBytes,
			RxPackets:      st.rxPackets,
			TxPackets:      st.txPackets,
			RxErrors:       st.rxErrors,
			TxErrors:       st.txErrors,
			RxDrops:        st.rxDrops,
			TxDrops:        st.txDrops,
		})
	}
	kindRank := func(k string) int {
		switch k {
		case "physical":
			return 0
		case "virtual":
			return 1
		default:
			return 2
		}
	}
	slices.SortFunc(out, func(a, b NetInterface) int {
		if a.Primary != b.Primary {
			if a.Primary {
				return -1
			}
			return 1
		}
		if r := cmp.Compare(kindRank(a.Kind), kindRank(b.Kind)); r != 0 {
			return r
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return out
}

func primaryRouteIP(network, target string) net.IP {
	c, err := net.Dial(network, target)
	if err != nil {
		return nil
	}
	defer c.Close()
	if ua, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return ua.IP
	}
	return nil
}

func readTrimmed(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
