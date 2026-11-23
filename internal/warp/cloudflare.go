// Package warp implements Cloudflare WARP account provisioning, WireGuard config parsing,
// routing rule parsing, and live tunnel verification through an isolated Xray process.
package warp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
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
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

const (
	TransportMasqueH3  = "masque-h3"
	TransportMasqueH2  = "masque-h2"
	TransportWireGuard = "wireguard"
	DefaultTransport   = TransportMasqueH3

	DefaultAPIBase         = "https://api.cloudflareclient.com/v0a4005"
	DefaultMasqueAPIBase   = "https://api.cloudflareclient.com/v0a4471"
	DefaultMasqueClientVer = "a-6.35-4471"
	DefaultEndpoint        = "engage.cloudflareclient.com:2408"
	DefaultMasqueEndpoint  = "162.159.198.2:443"
	DefaultMasqueSNI       = "consumer-masque.cloudflareclient.com"
	DefaultPeerPublicKey   = "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo="
	DefaultMTU             = 1280
	DefaultKeepalive       = 25
	DefaultDomainStrategy  = "ForceIPv4v6"
	DefaultTraceURL        = "https://www.cloudflare.com/cdn-cgi/trace"
)

// ValidTransports lists allowed WARP tunnel transports.
var ValidTransports = map[string]bool{
	TransportMasqueH3:  true,
	TransportMasqueH2:  true,
	TransportWireGuard: true,
}

// IsMasqueTransport reports whether t uses Xray's native MASQUE WARP transport (h3 or h2).
func IsMasqueTransport(t string) bool {
	return t == TransportMasqueH3 || t == TransportMasqueH2
}

// NormalizeTransport returns a valid WARP transport, defaulting to DefaultTransport ("masque-h3").
func NormalizeTransport(t string) string {
	t = strings.TrimSpace(strings.ToLower(t))
	if ValidTransports[t] {
		return t
	}
	return DefaultTransport
}

// IsMasqueKey reports whether secretKey holds a PEM-encoded ECDSA P-256 key enrolled for MASQUE.
func IsMasqueKey(secretKey string) bool {
	return strings.Contains(secretKey, "PRIVATE KEY")
}

// ValidDomainStrategies lists allowed Xray WireGuard/MASQUE domainStrategy values.
var ValidDomainStrategies = map[string]bool{
	"ForceIPv4v6": true,
	"ForceIPv4":   true,
	"ForceIPv6":   true,
	"ForceIPv6v4": true,
}

// Credentials holds Cloudflare WARP (MASQUE or WireGuard) or custom WireGuard tunnel parameters.
type Credentials struct {
	Transport     string `json:"transport,omitempty"` // "masque-h3" | "masque-h2" | "wireguard"
	CredMode      string `json:"credMode"`            // "auto" | "manual"
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

// Addresses returns non-empty interface CIDR addresses for Xray WireGuard/MASQUE outbound.
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

// NormalizeEndpoint ensures a WireGuard endpoint has a valid non-zero UDP port.
// Cloudflare WARP registration often returns "engage.cloudflareclient.com:0".
func NormalizeEndpoint(ep string) string {
	return NormalizeEndpointForTransport(ep, TransportWireGuard)
}

// NormalizeEndpointForTransport normalizes a WARP endpoint for either MASQUE (:443) or WireGuard (:2408),
// replacing stale default endpoints when switching between the two families.
func NormalizeEndpointForTransport(ep, transport string) string {
	ep = strings.TrimSpace(ep)
	if IsMasqueTransport(transport) {
		if ep == "" || strings.HasPrefix(strings.ToLower(ep), "engage.cloudflareclient.com") || strings.HasPrefix(ep, "162.159.192.") || strings.HasPrefix(ep, "162.159.193.") {
			return DefaultMasqueEndpoint
		}
		host, port, err := net.SplitHostPort(ep)
		if err != nil {
			return net.JoinHostPort(strings.Trim(ep, "[]"), "443")
		}
		if port == "" || port == "0" {
			port = "443"
		}
		return net.JoinHostPort(host, port)
	}
	if ep == "" || strings.HasPrefix(ep, "162.159.198.") || strings.HasPrefix(strings.ToLower(ep), "consumer-masque.") {
		return DefaultEndpoint
	}
	host, port, err := net.SplitHostPort(ep)
	if err != nil {
		return net.JoinHostPort(strings.Trim(ep, "[]"), "2408")
	}
	if port == "" || port == "0" {
		port = "2408"
	}
	return net.JoinHostPort(host, port)
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
				V4   string `json:"v4"`
				V6   string `json:"v6"`
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

// Provision registers a new Cloudflare WARP WireGuard device and optionally binds a WARP+ license key.
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
	hostName, _ := os.Hostname()
	body, _ := json.Marshal(map[string]any{
		"key":   pub,
		"tos":   time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"type":  "PC",
		"model": "vynel",
		"name":  hostName,
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
	if err := EnableWarp(ctx, client, apiBase, creds.DeviceID, creds.AccessToken); err != nil {
		return nil, err
	}
	if lk := strings.TrimSpace(licenseKey); lk != "" {
		if err := SetLicense(ctx, client, apiBase, creds, lk); err != nil {
			return creds, err
		}
	}
	return creds, nil
}

// ProvisionMasque registers a Cloudflare WARP device and enrolls an ECDSA P-256 key for Xray's
// native MASQUE (HTTP/3 QUIC over UDP 443 or HTTP/2 over TCP 443) transport.
func ProvisionMasque(ctx context.Context, client *http.Client, apiBase, licenseKey, transport string) (*Credentials, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if apiBase == "" {
		apiBase = DefaultMasqueAPIBase
	}
	apiBase = strings.TrimRight(apiBase, "/")
	if !IsMasqueTransport(transport) {
		transport = TransportMasqueH3
	}

	_, wgPub, err := GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	hostName, _ := os.Hostname()
	regBody, _ := json.Marshal(map[string]any{
		"key":   wgPub,
		"tos":   time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"type":  "PC",
		"model": "vynel",
		"name":  hostName,
	})
	regReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/reg", bytes.NewReader(regBody))
	if err != nil {
		return nil, err
	}
	setCFMasqueHeaders(regReq, "")
	regResp, err := client.Do(regReq)
	if err != nil {
		return nil, fmt.Errorf("cloudflare warp masque reg: %w", err)
	}
	regRaw, err := io.ReadAll(io.LimitReader(regResp.Body, 1<<20))
	regResp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read warp masque reg response: %w", err)
	}
	if regResp.StatusCode < 200 || regResp.StatusCode >= 300 {
		return nil, fmt.Errorf("cloudflare warp masque reg HTTP %d: %s", regResp.StatusCode, strings.TrimSpace(string(regRaw)))
	}
	var baseReg cfRegResponse
	if err := json.Unmarshal(regRaw, &baseReg); err != nil {
		return nil, fmt.Errorf("parse warp masque reg: %w", err)
	}
	if baseReg.ID == "" || baseReg.Token == "" {
		return nil, errors.New("cloudflare warp masque reg: missing id or token")
	}

	// Bind WARP+ license before enrolling MASQUE so the enrollment response carries updated account_type.
	if lk := strings.TrimSpace(licenseKey); lk != "" {
		licBody, _ := json.Marshal(map[string]string{"license": lk})
		licReq, err := http.NewRequestWithContext(ctx, http.MethodPut, apiBase+"/reg/"+baseReg.ID+"/account", bytes.NewReader(licBody))
		if err != nil {
			return nil, err
		}
		setCFMasqueHeaders(licReq, baseReg.Token)
		licResp, err := client.Do(licReq)
		if err != nil {
			return nil, fmt.Errorf("bind warp license: %w", err)
		}
		licRaw, _ := io.ReadAll(io.LimitReader(licResp.Body, 1<<20))
		licResp.Body.Close()
		if licResp.StatusCode < 200 || licResp.StatusCode >= 300 {
			return nil, fmt.Errorf("cloudflare license HTTP %d: %s", licResp.StatusCode, strings.TrimSpace(string(licRaw)))
		}
	}

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ecdsa p256 key: %w", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&ecKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("marshal ecdsa public key: %w", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(ecKey)
	if err != nil {
		return nil, fmt.Errorf("marshal ecdsa private key: %w", err)
	}
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}))
	pubB64 := base64.StdEncoding.EncodeToString(pubDER)

	enrollBody, _ := json.Marshal(map[string]string{
		"key":         pubB64,
		"key_type":    "secp256r1",
		"tunnel_type": "masque",
		"name":        hostName,
	})
	enReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, apiBase+"/reg/"+baseReg.ID, bytes.NewReader(enrollBody))
	if err != nil {
		return nil, err
	}
	setCFMasqueHeaders(enReq, baseReg.Token)
	enResp, err := client.Do(enReq)
	if err != nil {
		return nil, fmt.Errorf("cloudflare warp masque enroll: %w", err)
	}
	enRaw, err := io.ReadAll(io.LimitReader(enResp.Body, 1<<20))
	enResp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read warp masque enroll: %w", err)
	}
	if enResp.StatusCode < 200 || enResp.StatusCode >= 300 {
		return nil, fmt.Errorf("cloudflare warp masque enroll HTTP %d: %s", enResp.StatusCode, strings.TrimSpace(string(enRaw)))
	}
	var enrolled cfRegResponse
	if err := json.Unmarshal(enRaw, &enrolled); err != nil {
		return nil, fmt.Errorf("parse warp masque enroll: %w", err)
	}
	if len(enrolled.Config.Peers) == 0 || enrolled.Config.Peers[0].PublicKey == "" {
		return nil, errors.New("cloudflare warp masque enroll: missing peer public key")
	}
	peer := enrolled.Config.Peers[0]
	endpoint := DefaultMasqueEndpoint
	if peer.Endpoint.V4 != "" {
		endpoint = NormalizeEndpointForTransport(peer.Endpoint.V4, transport)
	}
	addrV4 := normalizeCIDR(enrolled.Config.Interface.Addresses.V4, 32)
	addrV6 := normalizeCIDR(enrolled.Config.Interface.Addresses.V6, 128)
	if addrV4 == "" && addrV6 == "" {
		return nil, errors.New("cloudflare не вернул IP-адреса интерфейса WARP MASQUE")
	}
	accType := enrolled.Account.AccountType
	if accType == "" {
		accType = baseReg.Account.AccountType
	}
	if accType == "" {
		accType = "free"
	}
	lic := strings.TrimSpace(licenseKey)
	if lic == "" {
		lic = enrolled.Account.License
	}
	if lic == "" {
		lic = baseReg.Account.License
	}
	reserved, _ := DecodeReserved(enrolled.Config.ClientID)
	return &Credentials{
		Transport:     transport,
		CredMode:      "auto",
		DeviceID:      baseReg.ID,
		AccessToken:   baseReg.Token,
		LicenseKey:    lic,
		AccountType:   accType,
		SecretKey:     privPEM,
		PublicKey:     pubB64,
		PeerPublicKey: peer.PublicKey,
		AddrV4:        addrV4,
		AddrV6:        addrV6,
		Reserved:      reserved,
		Endpoint:      endpoint,
		MTU:           DefaultMTU,
		Keepalive:     DefaultKeepalive,
	}, nil
}

// EnableWarp activates the WireGuard tunnel (warp_enabled=true) for a registered Cloudflare WARP device.
// Without this PATCH call, Cloudflare Edge silently drops WireGuard handshakes for newly created devices.
func EnableWarp(ctx context.Context, client *http.Client, apiBase, deviceID, accessToken string) error {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if apiBase == "" {
		apiBase = DefaultAPIBase
	}
	apiBase = strings.TrimRight(apiBase, "/")
	if deviceID == "" || accessToken == "" {
		return errors.New("missing deviceID or accessToken for WARP enable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, apiBase+"/reg/"+deviceID, bytes.NewReader([]byte(`{"warp_enabled":true}`)))
	if err != nil {
		return err
	}
	setCFHeaders(req, accessToken)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare warp enable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("cloudflare warp enable HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// SetLicense binds a WARP+ license key to an existing Cloudflare WARP device and refreshes its config.
func SetLicense(ctx context.Context, client *http.Client, apiBase string, creds *Credentials, licenseKey string) error {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if apiBase == "" {
		if IsMasqueTransport(creds.Transport) {
			apiBase = DefaultMasqueAPIBase
		} else {
			apiBase = DefaultAPIBase
		}
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
	if IsMasqueTransport(creds.Transport) {
		setCFMasqueHeaders(req, creds.AccessToken)
	} else {
		setCFHeaders(req, creds.AccessToken)
	}
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
	if IsMasqueTransport(creds.Transport) {
		setCFMasqueHeaders(getReq, creds.AccessToken)
	} else {
		setCFHeaders(getReq, creds.AccessToken)
	}
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
			endpoint = NormalizeEndpoint(reg.Config.Peers[0].Endpoint.Host)
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
		Transport:     TransportWireGuard,
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
	req.Header.Set("CF-Client-Version", "a-6.30-3596")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func setCFMasqueHeaders(req *http.Request, token string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("CF-Client-Version", DefaultMasqueClientVer)
	req.Header.Set("User-Agent", "WARP for Android")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

// ParseWireGuardConf parses an INI-style WireGuard / wgcf-profile.conf into Credentials.
func ParseWireGuardConf(raw string) (*Credentials, error) {
	creds := &Credentials{
		Transport:     TransportWireGuard,
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
			creds.Endpoint = NormalizeEndpoint(v)
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
	Transport      string   `json:"transport,omitempty"` // "masque-h3" | "masque-h2" | "wireguard"
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
	OK        bool   `json:"ok"`
	Transport string `json:"transport,omitempty"`
	IP        string `json:"ip,omitempty"`
	Colo      string `json:"colo,omitempty"`
	WarpMode  string `json:"warpMode,omitempty"` // on | plus | off
	RTTMs     int64  `json:"rttMs,omitempty"`
	Error     string `json:"error,omitempty"`
}

// CheckViaXray verifies a MASQUE or WireGuard WARP tunnel in complete isolation by starting a
// short-lived Xray process with a temporary loopback SOCKS5 inbound and requesting cdn-cgi/trace.
func CheckViaXray(ctx context.Context, xrayBin string, req CheckRequest) *CheckResult {
	res := &CheckResult{}
	if xrayBin == "" {
		res.Error = "бинарник xray не найден на ноде"
		return res
	}
	transport := req.Transport
	if transport == "" {
		if IsMasqueKey(req.SecretKey) {
			transport = TransportMasqueH3
		} else {
			transport = TransportWireGuard
		}
	}
	res.Transport = transport

	if req.SecretKey == "" || len(req.Addresses) == 0 {
		res.Error = "не заданы параметры туннеля WARP"
		return res
	}
	req.Endpoint = NormalizeEndpointForTransport(req.Endpoint, transport)
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

	var probeOutbound map[string]any
	if IsMasqueTransport(transport) {
		epHost, epPortStr, err := net.SplitHostPort(req.Endpoint)
		if err != nil {
			epHost = req.Endpoint
			epPortStr = "443"
		}
		epPort, err := strconv.Atoi(epPortStr)
		if err != nil || epPort <= 0 {
			epPort = 443
		}
		alpn := "h3"
		if transport == TransportMasqueH2 {
			alpn = "h2"
		}
		probeOutbound = map[string]any{
			"tag":            "probe-warp",
			"protocol":       "masque",
			"targetStrategy": req.DomainStrategy,
			"settings": map[string]any{
				"address": epHost,
				"port":    epPort,
			},
			"streamSettings": map[string]any{
				"network":  "masque",
				"security": "tls",
				"sockopt": map[string]any{
					"domainStrategy": req.DomainStrategy,
				},
				"tlsSettings": map[string]any{
					"serverName": DefaultMasqueSNI,
					"alpn":       []string{alpn},
				},
				"masqueSettings": map[string]any{
					"warp": map[string]any{
						"privateKey": req.SecretKey,
						"publicKey":  req.PeerPublicKey,
						"address":    req.Addresses,
					},
				},
			},
		}
	} else {
		if req.PeerPublicKey == "" {
			req.PeerPublicKey = DefaultPeerPublicKey
		}
		peer := map[string]any{
			"publicKey":  req.PeerPublicKey,
			"endpoint":   req.Endpoint,
			"keepAlive":  DefaultKeepalive,
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
		probeOutbound = map[string]any{
			"tag":            "probe-warp",
			"protocol":       "wireguard",
			"targetStrategy": req.DomainStrategy,
			"streamSettings": map[string]any{
				"sockopt": map[string]any{
					"domainStrategy": req.DomainStrategy,
				},
			},
			"settings": wgSettings,
		}
	}

	cfg := map[string]any{
		"log": map[string]any{"loglevel": "debug"},
		"inbounds": []any{
			map[string]any{
				"tag":      "probe-in",
				"listen":   "127.0.0.1",
				"port":     socksPort,
				"protocol": "socks",
				"settings": map[string]any{"udp": false},
			},
		},
		"outbounds": []any{probeOutbound},
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

	cctx, cancel := context.WithTimeout(ctx, 16*time.Second)
	defer cancel()

	var stderr bytes.Buffer
	cmd := exec.CommandContext(cctx, xrayBin, "run", "-c", cfgPath)
	cmd.Stdout = &stderr
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		res.Error = fmt.Sprintf("запуск xray: %v", err)
		return res
	}
	var stopOnce sync.Once
	stopCmd := func() {
		stopOnce.Do(func() {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
		})
	}
	defer stopCmd()

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
		stopCmd()
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			res.Error = fmt.Sprintf("xray не поднял тестовый туннель: %s", msg)
		} else {
			res.Error = "xray не поднял тестовый туннель за отведённое время"
		}
		return res
	}

	res = CheckViaSocks(cctx, socksAddr, traceURL, req.Endpoint)
	res.Transport = transport
	if !res.OK {
		stopCmd()
		if tail := summarizeXrayProbeLog(stderr.String()); tail != "" {
			res.Error += " | xray: " + tail
		}
	}
	return res
}

// CheckViaSocks performs a cdn-cgi/trace check through an already listening local SOCKS5 proxy.
func CheckViaSocks(ctx context.Context, socksAddr, traceURL, endpoint string) *CheckResult {
	res := &CheckResult{}
	if traceURL == "" {
		traceURL = DefaultTraceURL
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = DefaultEndpoint
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
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}
	defer tr.CloseIdleConnections()
	httpClient := &http.Client{Transport: tr, Timeout: 12 * time.Second}

	start := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, traceURL, nil)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		res.Error = fmt.Sprintf("туннель WARP (%s) не отвечает: %v", endpoint, err)
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

func summarizeXrayProbeLog(s string) string {
	lines := strings.Split(s, "\n")
	// Prefer explicit WireGuard/handshake/dial errors over generic "accepted" lines.
	priorities := []string{
		"Handshake did not complete",
		"Failed to send handshake",
		"failed to create TCP",
		"failed to dial",
		"Received handshake response",
		"Sending handshake initiation",
		"[Error]",
		"[Warning]",
	}
	for _, needle := range priorities {
		for i := len(lines) - 1; i >= 0; i-- {
			if t := strings.TrimSpace(lines[i]); strings.Contains(t, needle) {
				return t
			}
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}
