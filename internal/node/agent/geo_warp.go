package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/geostore"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/warp"
	"github.com/vyto4ka/vynel/internal/xray"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// CapGeo is the capability of agents that sync Snapshot.geo_assets into <DataDir>/geo.
const CapGeo = "geo"

// CapWarp is the capability of agents that handle ACTION_WARP_PROVISION and ACTION_WARP_CHECK.
const CapWarp = "warp"

// GeoFetcher streams a panel-managed .dat file (via gRPC FetchGeoAsset on remote nodes or
// direct file read on an all-in-one local node).
type GeoFetcher func(ctx context.Context, name, sha256 string) (io.ReadCloser, error)

// GRPCGeoFetcher returns a GeoFetcher backed by NodeGatewayClient.FetchGeoAsset.
func GRPCGeoFetcher(client nodev1.NodeGatewayClient) GeoFetcher {
	return func(ctx context.Context, name, wantSHA string) (io.ReadCloser, error) {
		stream, err := client.FetchGeoAsset(ctx, &nodev1.FetchGeoAssetRequest{Name: name, Sha256: wantSHA})
		if err != nil {
			return nil, err
		}
		pr, pw := io.Pipe()
		go func() {
			for {
				chunk, rerr := stream.Recv()
				if errors.Is(rerr, io.EOF) {
					_ = pw.Close()
					return
				}
				if rerr != nil {
					_ = pw.CloseWithError(rerr)
					return
				}
				if len(chunk.Data) > 0 {
					if _, werr := pw.Write(chunk.Data); werr != nil {
						return
					}
				}
			}
		}()
		return pr, nil
	}
}

func (a *Agent) geoDir() string {
	return filepath.Join(a.cfg.DataDir, "geo")
}

// ensureGeoDir creates <DataDir>/geo and symlinks system geosite.dat/geoip.dat from baseAssetDir
// when not overridden by panel-managed files.
func (a *Agent) ensureGeoDir() error {
	dir := a.geoDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	baseDir := a.baseAssetDir
	if baseDir == "" {
		baseDir = "/usr/local/share/xray"
	}
	if baseDir == dir {
		return nil
	}
	for _, name := range []string{geostore.SystemGeoSite, geostore.SystemGeoIP} {
		dst := filepath.Join(dir, name)
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		src := filepath.Join(baseDir, name)
		if st, err := os.Stat(src); err == nil && !st.IsDir() {
			_ = os.Symlink(src, dst)
		}
	}
	return nil
}

// syncGeoAssetsLocked downloads missing or updated .dat files into <DataDir>/geo and removes
// stale custom .dat files. It returns changed=true if any file on disk changed (requiring Xray restart).
func (a *Agent) syncGeoAssetsLocked(ctx context.Context, assets []*nodev1.GeoAsset) (changed bool, err error) {
	if err := a.ensureGeoDir(); err != nil {
		return false, err
	}
	dir := a.geoDir()
	want := make(map[string]*nodev1.GeoAsset, len(assets))
	for _, asset := range assets {
		clean, err := geostore.ValidateFileName(asset.Name)
		if err != nil {
			return false, fmt.Errorf("geo asset %q: %w", asset.Name, err)
		}
		want[clean] = asset
		dst := filepath.Join(dir, clean)

		// Check if a regular file with matching sha256 is already present.
		if lfi, lerr := os.Lstat(dst); lerr == nil && lfi.Mode().IsRegular() {
			if curHash, _, herr := geostore.HashFile(dst); herr == nil && curHash == asset.Sha256 {
				continue
			}
		}

		if a.cfg.FetchGeo == nil {
			return false, fmt.Errorf("geo asset %s required, but no geo fetcher is configured", clean)
		}
		fctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		rc, ferr := a.cfg.FetchGeo(fctx, clean, asset.Sha256)
		if ferr != nil {
			cancel()
			return false, fmt.Errorf("fetch geo asset %s: %w", clean, ferr)
		}
		if err := writeGeoAssetAtomic(dir, dst, rc, asset.Sha256, asset.Size); err != nil {
			rc.Close()
			cancel()
			return false, fmt.Errorf("write geo asset %s: %w", clean, err)
		}
		rc.Close()
		cancel()
		changed = true
	}

	// Remove stale panel-managed regular .dat files that are no longer needed.
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".dat") {
				continue
			}
			if _, keep := want[name]; keep {
				continue
			}
			p := filepath.Join(dir, name)
			if lfi, lerr := os.Lstat(p); lerr == nil && lfi.Mode().IsRegular() {
				if err := os.Remove(p); err == nil {
					changed = true
				}
			}
		}
	}
	// Restore system symlinks if a geosite.dat/geoip.dat override was removed.
	_ = a.ensureGeoDir()
	return changed, nil
}

func writeGeoAssetAtomic(dir, dst string, rc io.Reader, wantSHA string, wantSize int64) error {
	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	tmp := filepath.Join(dir, ".tmp-"+hex.EncodeToString(rnd[:]))
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		f.Close()
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(rc, geostore.MaxGeoFileBytes+1))
	if err != nil {
		return err
	}
	if n > geostore.MaxGeoFileBytes {
		return errors.New("geo asset exceeds size limit")
	}
	if wantSize > 0 && n != wantSize {
		return fmt.Errorf("size mismatch: got %d, want %d", n, wantSize)
	}
	gotSHA := hex.EncodeToString(h.Sum(nil))
	if wantSHA != "" && gotSHA != wantSHA {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", gotSHA, wantSHA)
	}
	if err := f.Close(); err != nil {
		return err
	}
	if lfi, err := os.Lstat(dst); err == nil && lfi.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(dst)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	cleanup = false
	return nil
}

type warpProvisionPayload struct {
	Transport  string            `json:"transport,omitempty"`
	LicenseKey string            `json:"licenseKey,omitempty"`
	Existing   *warp.Credentials `json:"existing,omitempty"`
}

func (a *Agent) handleWarpProvision(ctx context.Context, payload []byte) (string, error) {
	var req warpProvisionPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return "", fmt.Errorf("bad warp provision payload: %w", err)
		}
	}
	hc := &http.Client{Timeout: 20 * time.Second}
	var creds *warp.Credentials
	var err error
	if req.Existing != nil && req.Existing.DeviceID != "" && req.Existing.AccessToken != "" && req.LicenseKey != "" {
		creds = req.Existing
		err = warp.SetLicense(ctx, hc, "", creds, req.LicenseKey)
	} else if req.Transport == warp.TransportWireGuard {
		creds, err = warp.Provision(ctx, hc, "", req.LicenseKey)
	} else {
		desired := req.Transport
		if !warp.IsMasqueTransport(desired) {
			desired = warp.TransportMasqueH3
		}
		creds, err = warp.ProvisionMasque(ctx, hc, "", req.LicenseKey, desired)
		// When auto-selecting (or defaulting to masque-h3), verify UDP 443 QUIC on this host and
		// automatically switch to masque-h2 (TCP 443) if outbound UDP/QUIC is filtered.
		if err == nil && creds != nil && req.Transport != warp.TransportMasqueH2 && a.cfg.Xray.Path != "" {
			pctx1, cancel1 := context.WithTimeout(ctx, 7*time.Second)
			r1 := warp.CheckViaXray(pctx1, a.cfg.Xray.Path, warp.CheckRequest{
				Transport:      warp.TransportMasqueH3,
				SecretKey:      creds.SecretKey,
				Addresses:      creds.Addresses(),
				PeerPublicKey:  creds.PeerPublicKey,
				Endpoint:       creds.Endpoint,
				MTU:            creds.MTU,
				DomainStrategy: warp.DefaultDomainStrategy,
			})
			cancel1()
			if !r1.OK {
				pctx2, cancel2 := context.WithTimeout(ctx, 8*time.Second)
				r2 := warp.CheckViaXray(pctx2, a.cfg.Xray.Path, warp.CheckRequest{
					Transport:      warp.TransportMasqueH2,
					SecretKey:      creds.SecretKey,
					Addresses:      creds.Addresses(),
					PeerPublicKey:  creds.PeerPublicKey,
					Endpoint:       creds.Endpoint,
					MTU:            creds.MTU,
					DomainStrategy: warp.DefaultDomainStrategy,
				})
				cancel2()
				if r2.OK {
					creds.Transport = warp.TransportMasqueH2
				}
			}
		}
	}
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(creds)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (a *Agent) handleWarpCheck(ctx context.Context, payload []byte) (string, error) {
	var req warp.CheckRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return "", fmt.Errorf("bad warp check payload: %w", err)
	}
	var res *warp.CheckResult
	if liveRes, usedLive := a.checkWarpViaLiveXray(ctx, req); usedLive {
		res = liveRes
	} else {
		res = warp.CheckViaXray(ctx, a.cfg.Xray.Path, req)
	}
	b, err := json.Marshal(res)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// checkWarpViaLiveXray reuses the running main Xray's VYNEL_WARP outbound when it is already
// active with the same transport, secretKey, and endpoint.
func (a *Agent) checkWarpViaLiveXray(ctx context.Context, req warp.CheckRequest) (*warp.CheckResult, bool) {
	a.mu.Lock()
	var api *xray.API
	var activeCfg []byte
	if a.proc != nil && a.proc.Running() && a.api != nil && a.cur != nil {
		api = a.api
		activeCfg = a.cur.Config
	}
	a.mu.Unlock()

	if api == nil || !activeConfigHasMatchingWarp(activeCfg, req) {
		return nil, false
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, false
	}
	socksAddr := lis.Addr().String()
	_, portStr, _ := net.SplitHostPort(socksAddr)
	port, err := strconv.Atoi(portStr)
	_ = lis.Close()
	if err != nil || port <= 0 {
		return nil, false
	}

	addCtx, addCancel := context.WithTimeout(ctx, 3*time.Second)
	_ = api.RemoveInbound(addCtx, xrayconf.WarpProbeTag)
	err = api.AddLoopbackSocksInbound(addCtx, xrayconf.WarpProbeTag, uint32(port))
	addCancel()
	if err != nil {
		return nil, false
	}
	defer func() {
		rmCtx, rmCancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = api.RemoveInbound(rmCtx, xrayconf.WarpProbeTag)
		rmCancel()
	}()

	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res := warp.CheckViaSocks(pctx, socksAddr, req.TraceURL, req.Endpoint)
	res.Transport = req.Transport
	return res, true
}

func activeConfigHasMatchingWarp(rawCfg []byte, req warp.CheckRequest) bool {
	if len(rawCfg) == 0 || req.SecretKey == "" {
		return false
	}
	var cfg struct {
		Outbounds []struct {
			Tag      string `json:"tag"`
			Protocol string `json:"protocol"`
			Settings struct {
				SecretKey string `json:"secretKey"`
				Address   string `json:"address"`
				Port      int    `json:"port"`
				Peers     []struct {
					Endpoint string `json:"endpoint"`
				} `json:"peers"`
			} `json:"settings"`
			StreamSettings struct {
				TLSSettings struct {
					ALPN []string `json:"alpn"`
				} `json:"tlsSettings"`
				MasqueSettings struct {
					Warp struct {
						PrivateKey string `json:"privateKey"`
					} `json:"warp"`
				} `json:"masqueSettings"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
		Routing struct {
			Rules []struct {
				InboundTag  []string `json:"inboundTag"`
				OutboundTag string   `json:"outboundTag"`
			} `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(rawCfg, &cfg); err != nil {
		return false
	}
	transport := req.Transport
	if transport == "" {
		if warp.IsMasqueKey(req.SecretKey) {
			transport = warp.TransportMasqueH3
		} else {
			transport = warp.TransportWireGuard
		}
	}
	wantEP := warp.NormalizeEndpointForTransport(req.Endpoint, transport)
	hasOutbound := false
	for _, ob := range cfg.Outbounds {
		if ob.Tag != xrayconf.WarpTag {
			continue
		}
		if warp.IsMasqueTransport(transport) {
			if ob.Protocol != "masque" || ob.StreamSettings.MasqueSettings.Warp.PrivateKey != req.SecretKey {
				return false
			}
			wantALPN := "h3"
			if transport == warp.TransportMasqueH2 {
				wantALPN = "h2"
			}
			if len(ob.StreamSettings.TLSSettings.ALPN) == 0 || ob.StreamSettings.TLSSettings.ALPN[0] != wantALPN {
				return false
			}
			curEP := net.JoinHostPort(ob.Settings.Address, strconv.Itoa(ob.Settings.Port))
			if warp.NormalizeEndpointForTransport(curEP, transport) != wantEP {
				return false
			}
		} else {
			if ob.Protocol != "wireguard" || ob.Settings.SecretKey != req.SecretKey || len(ob.Settings.Peers) == 0 {
				return false
			}
			if warp.NormalizeEndpoint(ob.Settings.Peers[0].Endpoint) != wantEP {
				return false
			}
		}
		hasOutbound = true
		break
	}
	if !hasOutbound {
		return false
	}
	for _, r := range cfg.Routing.Rules {
		if r.OutboundTag != xrayconf.WarpTag {
			continue
		}
		for _, tag := range r.InboundTag {
			if tag == xrayconf.WarpProbeTag {
				return true
			}
		}
	}
	return false
}
