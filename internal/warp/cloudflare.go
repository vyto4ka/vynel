// Package warp implements Cloudflare WARP account provisioning, WireGuard config parsing,
// routing rule parsing, and live tunnel verification through an isolated Xray process.
package warp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

const (
	DefaultAPIBase        = "https://api.cloudflareclient.com/v0a2158"
	DefaultEndpoint       = "engage.cloudflareclient.com:2408"
	DefaultPeerPublicKey  = "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo="
	DefaultMTU            = 1280
	DefaultKeepalive      = 25
	DefaultDomainStrategy = "ForceIPv4v6"
	DefaultTraceURL       = "https://www.cloudflare.com/cdn-cgi/trace"
)

// ValidDomainStrategies lists allowed Xray WireGuard domainStrategy values.
var ValidDomainStrategies = map[string]bool{
	"ForceIPv4v6": true,
	"ForceIPv4":   true,
	"ForceIPv6":   true,
	"ForceIPv6v4": true,
}

// Credentials holds Cloudflare WARP or custom WireGuard tunnel parameters.
type Credentials struct {
	CredMode      string `json:"credMode"` // "auto" | "manual"
	DeviceID      string `json:"deviceId,omitempty"`
	AccessToken   string `json:"accessToken,omitempty"`
	LicenseKey    string `json:"licenseKey,omitempty"`
	AccountType   string `json:"accountType,omitempty"` // free | limited | unlimited | custom
	SecretKey     string `json:"secretKey"`
	PublicKey     string `json:"publicKey,omitempty"`
	PeerPublicKey string `json:"peerPublicKey"`
	PresharedKey  string `json:"presharedKey,omitempty"`
	AddrV4        string `json:"addrV4,omitempty"`
	AddrV6        string `json:"addrV6,omitempty"`
	Reserved      []int  `json:"reserved,omitempty"`
	Endpoint      string `json:"endpoint"`
	MTU           int    `json:"mtu"`
	Keepalive     int    `json:"keepalive"`
}

// Addresses returns non-empty interface CIDR addresses for Xray WireGuard outbound.
func (c *Credentials) Addresses() []string {
	var out []string
	if a := normalizeCIDR(c.AddrV4, 32); a != "" {
		out = append(out, a)
	}
	if a := normalizeCIDR(c.AddrV6, 128); a != "" {
		out = append(out, a)
	}
	return out
}

func normalizeCIDR(s string, defaultBits int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "/") {
		return s
	}
	if ip := net.ParseIP(s); ip != nil {
		return fmt.Sprintf("%s/%d", ip.String(), defaultBits)
	}
	return s
}

// GenerateKeyPair creates a WireGuard X25519 key pair in standard base64 encoding.
func GenerateKeyPair() (privB64, pubB64 string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate x25519 key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(k.Bytes()),
		base64.StdEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

// DecodeReserved converts Cloudflare WARP base64 client_id into a 3-byte integer slice.
func DecodeReserved(clientID string) ([]int, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(clientID)
	if err != nil {
		return nil, fmt.Errorf("decode client_id: %w", err)
	}
	if len(raw) != 3 {
		return nil, fmt.Errorf("unexpected client_id length %d (expected 3 bytes)", len(raw))
	}
	return []int{int(raw[0]), int(raw[1]), int(raw[2])}, nil
}

type cfRegResponse struct {
	ID      string `json:"id"`
	Token   string `json:"token"`
	Account struct {
		AccountType string `json:"account_type"`
		License     string `json:"license"`
	} `json:"account"`
	Config struct {
		ClientID string `json:"client_id"`
		Peers    []struct {
			PublicKey string `json:"public_key"`
			Endpoint  struct {
				Host string `json:"host"`
			} `json:"endpoint"`
		} `json:"peers"`
		Interface struct {
			Addresses struct {
				V4 string `json:"v4"`
				V6 string `json:"v6"`
			} `json:"addresses"`
		} `json:"interface"`
	} `json:"config"`
}

// Provision registers a new Cloudflare WARP device and optionally binds a WARP+ license key.
func Provision(ctx context.Context, client *http.Client, apiBase, licenseKey string) (*Credentials, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if apiBase == "" {
		apiBase = DefaultAPIBase
	}
	apiBase = strings.TrimRight(apiBase, "/")

	priv, pub, err := GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	var serial [8]byte
	_, _ = rand.Read(serial[:])

	body, _ := json.Marshal(map[string]any{
		"key":           pub,
		"install_id":    "",
		"fcm_token":     "",
		"tos":           time.Now().UTC().Format(time.RFC3339Nano),
		"model":         "PC",
		"serial_number": hex.EncodeToString(serial[:]),
		"locale":        "en_US",
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/reg", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	setCFHeaders(req, "")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloudflare warp reg: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read warp reg response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("cloudflare warp api HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var reg cfRegResponse
	if err := json.Unmarshal(raw, &reg); err != nil {
		return nil, fmt.Errorf("parse warp reg response: %w", err)
	}
	creds, err := credentialsFromCF(&reg, priv, pub)
	if err != nil {
		return nil, err
	}
	if lk := strings.TrimSpace(licenseKey); lk != "" {
		if err := SetLicense(ctx, client, apiBase, creds, lk); err != nil {
			return creds, err
		}
	}
	return creds, nil
}

// SetLicense binds a WARP+ license key to an existing Cloudflare WARP device and refreshes its config.
func SetLicense(ctx context.Context, client *http.Client, apiBase string, creds *Credentials, licenseKey string) error {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if apiBase == "" {
		apiBase = DefaultAPIBase
	}
	apiBase = strings.TrimRight(apiBase, "/")
	if creds.DeviceID == "" || creds.AccessToken == "" {
		return errors.New("для привязки ключа WARP+ нужен автоматически зарегистрированный аккаунт")
	}

	body, _ := json.Marshal(map[string]string{"license": strings.TrimSpace(licenseKey)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, apiBase+"/reg/"+creds.DeviceID+"/account", bytes.NewReader(body))
	if err != nil {
		return err
	}
	setCFHeaders(req, creds.AccessToken)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("bind warp license: %w", err)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("cloudflare license HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	// Re-fetch device registration to refresh account_type, addresses and reserved bytes.
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/reg/"+creds.DeviceID, nil)
	if err != nil {
		return err
	}
	setCFHeaders(getReq, creds.AccessToken)
	getResp, err := client.Do(getReq)
	if err != nil {
		return fmt.Errorf("refresh warp device: %w", err)
	}
	defer getResp.Body.Close()
	getRaw, _ := io.ReadAll(io.LimitReader(getResp.Body, 1<<20))
	if getResp.StatusCode < 200 || getResp.StatusCode >= 300 {
		return fmt.Errorf("cloudflare refresh HTTP %d: %s", getResp.StatusCode, strings.TrimSpace(string(getRaw)))
	}
	var reg cfRegResponse
	if err := json.Unmarshal(getRaw, &reg); err != nil {
		return fmt.Errorf("parse warp refresh: %w", err)
	}
	if reg.Account.AccountType != "" {
		creds.AccountType = reg.Account.AccountType
	}
	creds.LicenseKey = strings.TrimSpace(licenseKey)
	if reg.Config.Interface.Addresses.V4 != "" {
		creds.AddrV4 = normalizeCIDR(reg.Config.Interface.Addresses.V4, 32)
	}
	if reg.Config.Interface.Addresses.V6 != "" {
		creds.AddrV6 = normalizeCIDR(reg.Config.Interface.Addresses.V6, 128)
	}
	if res, err := DecodeReserved(reg.Config.ClientID); err == nil && len(res) == 3 {
		creds.Reserved = res
	}
	return nil
}

func credentialsFromCF(reg *cfRegResponse, priv, pub string) (*Credentials, error) {
	peerPub := DefaultPeerPublicKey
	endpoint := DefaultEndpoint
	if len(reg.Config.Peers) > 0 {
		if reg.Config.Peers[0].PublicKey != "" {
			peerPub = reg.Config.Peers[0].PublicKey
		}
		if reg.Config.Peers[0].Endpoint.Host != "" {
			endpoint = reg.Config.Peers[0].Endpoint.Host
		}
	}
	reserved, err := DecodeReserved(reg.Config.ClientID)
	if err != nil {
		return nil, err
	}
	accType := reg.Account.AccountType
	if accType == "" {
		accType = "free"
	}
	addrV4 := normalizeCIDR(reg.Config.Interface.Addresses.V4, 32)
	addrV6 := normalizeCIDR(reg.Config.Interface.Addresses.V6, 128)
	if addrV4 == "" && addrV6 == "" {
		return nil, errors.New("cloudflare не вернул IP-адреса интерфейса WARP")
	}
	return &Credentials{
		CredMode:      "auto",
		DeviceID:      reg.ID,
		AccessToken:   reg.Token,
		LicenseKey:    reg.Account.License,
		AccountType:   accType,
		SecretKey:     priv,
		PublicKey:     pub,
		PeerPublicKey: peerPub,
		AddrV4:        addrV4,
		AddrV6:        addrV6,
		Reserved:      reserved,
		Endpoint:      endpoint,
		MTU:           DefaultMTU,
		Keepalive:     DefaultKeepalive,
	}, nil
}

func setCFHeaders(req *http.Request, token string) {
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("User-Agent", "okhttp/3.12.1")
	req.Header.Set("CF-Client-Version", "a-6.30-2158")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

// ParseWireGuardConf parses an INI-style WireGuard / wgcf-profile.conf into Credentials.
func ParseWireGuardConf(raw string) (*Credentials, error) {
	creds := &Credentials{
		CredMode:      "manual",
		AccountType:   "custom",
		PeerPublicKey: DefaultPeerPublicKey,
		Endpoint:      DefaultEndpoint,
		MTU:           DefaultMTU,
		Keepalive:     DefaultKeepalive,
	}
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			// Support optional "# Reserved = [1, 2, 3]" comment from wgcf exporters.
			trimmed := strings.TrimSpace(strings.TrimLeft(line, "#;"))
			if strings.HasPrefix(strings.ToLower(trimmed), "reserved") {
				line = trimmed
			} else {
				continue
			}
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		switch k {
		case "privatekey":
			creds.SecretKey = v
		case "publickey":
			creds.PeerPublicKey = v
		case "presharedkey":
			creds.PresharedKey = v
		case "endpoint":
			creds.Endpoint = v
		case "mtu":
			if n, err := strconv.Atoi(v); err == nil && n >= 576 && n <= 9000 {
				creds.MTU = n
			}
		case "persistentkeepalive", "keepalive":
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 3600 {
				creds.Keepalive = n
			}
		case "address":
			for _, part := range strings.Split(v, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				if strings.Contains(part, ":") {
					creds.AddrV6 = normalizeCIDR(part, 128)
				} else {
					creds.AddrV4 = normalizeCIDR(part, 32)
				}
			}
		case "reserved":
			if res, err := ParseReserved(v); err == nil {
				creds.Reserved = res
			}
		}
	}
	if creds.SecretKey == "" {
		return nil, errors.New("в WireGuard-конфиге не найден PrivateKey")
	}
	if creds.AddrV4 == "" && creds.AddrV6 == "" {
		return nil, errors.New("в WireGuard-конфиге не найден Address")
	}
	if creds.Endpoint == "" {
		return nil, errors.New("в WireGuard-конфиге не найден Endpoint")
	}
	return creds, nil
}

// ParseReserved parses reserved bytes from "[1, 2, 3]", "1,2,3", or base64 client_id.
func ParseReserved(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil, nil
	}
	clean := strings.Trim(s, "[] ")
	if strings.Contains(clean, ",") {
		parts := strings.Split(clean, ",")
		if len(parts) != 3 {
			return nil, errors.New("reserved должен содержать ровно 3 числа от 0 до 255")
		}
		out := make([]int, 3)
		for i, p := range parts {
			n, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil || n < 0 || n > 255 {
				return nil, fmt.Errorf("некорректный байт reserved: %q", p)
			}
			out[i] = n
		}
		return out, nil
	}
	return DecodeReserved(s)
}

// CheckRequest carries tunnel parameters for ACTION_WARP_CHECK on a node.
type CheckRequest struct {
	SecretKey      string   `json:"secretKey"`
	Addresses      []string `json:"addresses"`
	PeerPublicKey  string   `json:"peerPublicKey"`
	PresharedKey   string   `json:"presharedKey,omitempty"`
	Reserved       []int    `json:"reserved,omitempty"`
	Endpoint       string   `json:"endpoint"`
	MTU            int      `json:"mtu"`
	DomainStrategy string   `json:"domainStrategy"`
	TraceURL       string   `json:"traceUrl,omitempty"`
}

// CheckResult is returned by ACTION_WARP_CHECK.
type CheckResult struct {
	OK       bool   `json:"ok"`
	IP       string `json:"ip,omitempty"`
	Colo     string `json:"colo,omitempty"`
	WarpMode string `json:"warpMode,omitempty"` // on | plus | off
	RTTMs    int64  `json:"rttMs,omitempty"`
	Error    string `json:"error,omitempty"`
}

// CheckViaXray verifies a WireGuard/WARP tunnel in complete isolation by starting a short-lived
// Xray process with a temporary loopback SOCKS5 inbound and requesting Cloudflare's cdn-cgi/trace.
func CheckViaXray(ctx context.Context, xrayBin string, req CheckRequest) *CheckResult {
	res := &CheckResult{}
	if xrayBin == "" {
		res.Error = "бинарник xray не найден на ноде"
		return res
	}
	if req.SecretKey == "" || len(req.Addresses) == 0 || req.Endpoint == "" {
		res.Error = "не заданы параметры туннеля WARP"
		return res
	}
	if req.PeerPublicKey == "" {
		req.PeerPublicKey = DefaultPeerPublicKey
	}
	if req.MTU <= 0 {
		req.MTU = DefaultMTU
	}
	if !ValidDomainStrategies[req.DomainStrategy] {
		req.DomainStrategy = DefaultDomainStrategy
	}
	traceURL := req.TraceURL
	if traceURL == "" {
		traceURL = DefaultTraceURL
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		res.Error = fmt.Sprintf("выделение локального порта: %v", err)
		return res
	}
	socksAddr := lis.Addr().String()
	_, portStr, _ := net.SplitHostPort(socksAddr)
	socksPort, _ := strconv.Atoi(portStr)
	_ = lis.Close()

	peer := map[string]any{
		"publicKey":  req.PeerPublicKey,
		"endpoint":   req.Endpoint,
		"allowedIPs": []string{"0.0.0.0/0", "::/0"},
	}
	if req.PresharedKey != "" {
		peer["preSharedKey"] = req.PresharedKey
	}
	wgSettings := map[string]any{
		"noKernelTun":    true,
		"secretKey":      req.SecretKey,
		"address":        req.Addresses,
		"peers":          []any{peer},
		"mtu":            req.MTU,
		"domainStrategy": req.DomainStrategy,
	}
	if len(req.Reserved) == 3 {
		wgSettings["reserved"] = req.Reserved
	}
	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{
			map[string]any{
				"tag":      "probe-in",
				"listen":   "127.0.0.1",
				"port":     socksPort,
				"protocol": "socks",
				"settings": map[string]any{"udp": false},
			},
		},
		"outbounds": []any{
			map[string]any{
				"tag":      "probe-warp",
				"protocol": "wireguard",
				"settings": wgSettings,
			},
		},
	}

	tmpDir, err := os.MkdirTemp("", "vynel-warp-check-")
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer os.RemoveAll(tmpDir)
	cfgPath := filepath.Join(tmpDir, "probe.json")
	rawCfg, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, rawCfg, 0o600); err != nil {
		res.Error = err.Error()
		return res
	}

	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var stderr bytes.Buffer
	cmd := exec.CommandContext(cctx, xrayBin, "run", "-c", cfgPath)
	cmd.Stdout = &stderr
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		res.Error = fmt.Sprintf("запуск xray: %v", err)
		return res
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	// Wait until the temporary SOCKS5 listener accepts connections.
	ready := false
	for i := 0; i < 30; i++ {
		if cctx.Err() != nil {
			break
		}
		conn, err := net.DialTimeout("tcp", socksAddr, 150*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			res.Error = fmt.Sprintf("xray не поднял тестовый туннель: %s", msg)
		} else {
			res.Error = "xray не поднял тестовый туннель за отведённое время"
		}
		return res
	}

	dialer, err := proxy.SOCKS5("tcp", socksAddr, nil, proxy.Direct)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	tr := &http.Transport{
		DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		},
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}
	defer tr.CloseIdleConnections()
	httpClient := &http.Client{Transport: tr, Timeout: 12 * time.Second}

	start := time.Now()
	httpReq, err := http.NewRequestWithContext(cctx, http.MethodGet, traceURL, nil)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		res.Error = fmt.Sprintf("туннель WARP не отвечает: %v", err)
		return res
	}
	defer resp.Body.Close()
	res.RTTMs = time.Since(start).Milliseconds()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if resp.StatusCode != http.StatusOK {
		res.Error = fmt.Sprintf("cloudflare trace HTTP %d", resp.StatusCode)
		return res
	}
	for _, line := range strings.Split(string(body), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "ip":
			res.IP = v
		case "colo":
			res.Colo = v
		case "warp":
			res.WarpMode = v
		}
	}
	if res.IP == "" {
		res.Error = "некорректный ответ cdn-cgi/trace"
		return res
	}
	res.OK = true
	return res
}
