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
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/geostore"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/warp"
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
	} else {
		creds, err = warp.Provision(ctx, hc, "", req.LicenseKey)
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
	res := warp.CheckViaXray(ctx, a.cfg.Xray.Path, req)
	b, err := json.Marshal(res)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
