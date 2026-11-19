package subscription

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

// Karing imports servers and remote rule-set/outbound pairs rather than running the supplied
// config. In particular, inbounds, DNS, inline rule sets and action:reject are not portable
// subscription settings. Its importer recognizes the legacy block outbound as the Block target.
// https://github.com/KaringX/karing/blob/e3c19f1eb1225dbd48a4862f0983b82d0af8de69/lib/app/utils/singbox_json_utils.dart
// https://karing.app/en/app-manual/diversion-rule/
func Karing(hosts []service.Host, uuid, subURL string, r service.ClientRouting) ([]byte, error) {
	var proxies []map[string]any
	var named []service.Host
	for _, h := range hosts {
		var p map[string]any
		if h.Raw != "" {
			var ok bool
			p, ok = karingLink(h.Raw)
			if !ok {
				continue
			}
			h.Remark, _ = p["tag"].(string)
		} else {
			out, _ := singBoxOutbounds([]service.Host{h}, uuid)
			p = out[0]
			if h.Network == "xhttp" {
				p["transport"] = karingXHTTP(h)
			}
		}
		if h.Remark == "" {
			h.Remark = h.Address
		}
		named = append(named, h)
		proxies = append(proxies, p)
	}
	names := uniqueNames(named, "proxy", "direct", "block")
	for i := range proxies {
		proxies[i]["tag"] = names[i]
	}
	outbounds := []map[string]any{
		{"type": "selector", "tag": "proxy", "outbounds": names},
		{"type": "direct", "tag": "direct"},
		{"type": "block", "tag": "block"},
	}
	outbounds = append(outbounds, proxies...)
	route, _ := karingRouting(subURL, r)
	return json.MarshalIndent(map[string]any{"outbounds": outbounds, "route": route}, "", "  ")
}

var karingVerRe = regexp.MustCompile(`(?i)karing/(\d+)\.(\d+)(?:\.(\d+))?`)

// karingOlderThan reports whether a Karing User-Agent explicitly advertises a version below major.minor.patch.
// A User-Agent without a version number is treated as current.
func karingOlderThan(ua string, major, minor, patch int) bool {
	m := karingVerRe.FindStringSubmatch(ua)
	if len(m) < 3 {
		return false
	}
	ma, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	pa := 0
	if len(m) >= 4 && m[3] != "" {
		pa, _ = strconv.Atoi(m[3])
	}
	if ma != major {
		return ma < major
	}
	if mi != minor {
		return mi < minor
	}
	return pa < patch
}

// karingNeeds1226 reports whether an XHTTP host uses cookie/query obfuscation placement fields
// introduced when Karing moved to sing-box-extended in 1.2.26.
func karingNeeds1226(h service.Host) bool {
	if h.Network != "xhttp" || len(h.Extra) == 0 {
		return false
	}
	if obfs, _ := h.Extra["xPaddingObfsMode"].(bool); obfs {
		return true
	}
	for _, k := range []string{"sessionPlacement", "sessionIDPlacement", "seqPlacement", "xPaddingPlacement"} {
		if v, _ := h.Extra[k].(string); v == "cookie" || v == "query" || v == "header" {
			return true
		}
	}
	return false
}

// karingXHTTPKeys maps Xray xhttp extra keys to KaringX/sing-box (sing-box-extended) snake_case keys.
var karingXHTTPKeys = map[string]string{
	"noGRPCHeader": "no_grpc_header", "xPaddingBytes": "x_padding_bytes", "xPaddingObfsMode": "x_padding_obfs_mode",
	"xPaddingKey": "x_padding_key", "xPaddingHeader": "x_padding_header", "xPaddingPlacement": "x_padding_placement",
	"xPaddingMethod": "x_padding_method", "uplinkHTTPMethod": "uplink_http_method", "sessionPlacement": "session_placement",
	"sessionKey": "session_key", "seqPlacement": "seq_placement", "seqKey": "seq_key",
	"uplinkDataPlacement": "uplink_data_placement", "uplinkDataKey": "uplink_data_key", "uplinkChunkSize": "uplink_chunk_size",
	"scMaxEachPostBytes": "sc_max_each_post_bytes", "scMinPostsIntervalMs": "sc_min_posts_interval_ms",
	"scMaxBufferedPosts": "sc_max_buffered_posts", "scStreamUpServerSecs": "sc_stream_up_server_secs",
	"headers": "headers",
}

var karingXmuxKeys = map[string]string{
	"maxConcurrency": "max_concurrency", "maxConnections": "max_connections", "cMaxReuseTimes": "c_max_reuse_times",
	"hMaxRequestTimes": "h_max_request_times", "hMaxReusableSecs": "h_max_reusable_secs", "hKeepAlivePeriod": "h_keep_alive_period",
}

// karingXHTTP renders an XHTTP transport block for Karing's sing-box-extended core.
func karingXHTTP(h service.Host) map[string]any {
	path := h.Path
	if path == "" {
		path = "/"
	} else if !strings.HasSuffix(path, "/") && !strings.Contains(path, "?") {
		path += "/"
	}
	t := map[string]any{"type": "xhttp", "path": path}
	if h.HostHeader != "" {
		t["host"] = h.HostHeader
	}
	mode := h.Mode
	if m, ok := h.Extra["mode"].(string); ok && m != "" {
		mode = m
	}
	if mode != "" {
		t["mode"] = mode
	}
	applyKaringXHTTPExtra(t, h.Extra)
	if mode != "stream-one" {
		if ds, ok := h.Extra["downloadSettings"].(map[string]any); ok {
			if kd := karingXHTTPDownload(ds); len(kd) > 0 {
				t["download"] = kd
			}
		}
	}
	return t
}

func applyKaringXHTTPExtra(dst, extra map[string]any) {
	for k, v := range extra {
		if kk, ok := karingXHTTPKeys[k]; ok {
			asStr := kk == "x_padding_bytes" || kk == "sc_max_each_post_bytes" || kk == "sc_min_posts_interval_ms" || kk == "sc_stream_up_server_secs"
			if asStr {
				switch n := v.(type) {
				case int, int64, float64:
					dst[kk] = fmt.Sprint(n)
					continue
				}
			}
			dst[kk] = v
		}
	}
	if xm, ok := extra["xmux"].(map[string]any); ok {
		out := map[string]any{}
		for k, v := range xm {
			if kk, ok := karingXmuxKeys[k]; ok {
				if kk != "h_keep_alive_period" {
					switch n := v.(type) {
					case int, int64, float64:
						out[kk] = fmt.Sprint(n)
						continue
					}
				}
				out[kk] = v
			}
		}
		// sing-box-extended forbids setting both max_concurrency and max_connections at once.
		if _, hasConc := out["max_concurrency"]; hasConc {
			delete(out, "max_connections")
		}
		if len(out) > 0 {
			dst["xmux"] = out
		}
	}
}

func karingXHTTPDownload(ds map[string]any) map[string]any {
	out := map[string]any{}
	if v, ok := ds["address"].(string); ok && v != "" {
		out["server"] = v
	}
	if v, ok := ds["port"]; ok {
		out["server_port"] = v
	}
	sec, _ := ds["security"].(string)
	switch sec {
	case "tls":
		tls := map[string]any{"enabled": true}
		if ts, ok := ds["tlsSettings"].(map[string]any); ok {
			if sni, ok := ts["serverName"].(string); ok && sni != "" {
				tls["server_name"] = sni
			}
			if fp, ok := ts["fingerprint"].(string); ok && fp != "" {
				tls["utls"] = map[string]any{"enabled": true, "fingerprint": singBoxFingerprint(fp)}
			}
			if alpn, ok := ts["alpn"]; ok {
				tls["alpn"] = alpn
			}
		}
		out["tls"] = tls
	case "reality":
		tls := map[string]any{"enabled": true}
		if rs, ok := ds["realitySettings"].(map[string]any); ok {
			if sni, ok := rs["serverName"].(string); ok && sni != "" {
				tls["server_name"] = sni
			}
			fp, _ := rs["fingerprint"].(string)
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": singBoxFingerprint(fp)}
			pbk, _ := rs["publicKey"].(string)
			sid, _ := rs["shortId"].(string)
			if pbk != "" {
				tls["reality"] = map[string]any{"enabled": true, "public_key": pbk, "short_id": sid}
			}
		}
		out["tls"] = tls
	}
	if xs, ok := ds["xhttpSettings"].(map[string]any); ok {
		if path, ok := xs["path"].(string); ok && path != "" {
			if !strings.HasSuffix(path, "/") && !strings.Contains(path, "?") {
				path += "/"
			}
			out["path"] = path
		}
		if host, ok := xs["host"].(string); ok && host != "" {
			out["host"] = host
		}
		if extra, ok := xs["extra"].(map[string]any); ok {
			applyKaringXHTTPExtra(out, extra)
		}
	}
	return out
}

// karingRouting exposes ordinary matchers as source rule sets and geo categories as remote
// binary rule sets. Every imported rule has exactly two keys: rule_set and outbound. Splitting
// them by target preserves priority: block, explicit proxy, private, RU, direct, final.
func karingRouting(subURL string, r service.ClientRouting) (map[string]any, map[string][]map[string]any) {
	s := &singRoute{ruleSets: map[string]map[string]any{}}
	sources := map[string][]map[string]any{}
	var rules []map[string]any
	add := func(name, target string, entries []string, extra ...map[string]any) {
		var local []string
		var refs []string
		for _, e := range entries {
			entry := service.ParseRuleEntry(e)
			if entry.Kind == "geosite" || entry.Kind == "geoip" {
				refs = append(refs, s.set(entry.Kind, entry.Value))
			} else {
				local = append(local, e)
			}
		}
		matches := append(s.match(local), extra...)
		if len(matches) > 0 {
			sources[name] = matches
			tag := "vynel-" + name
			data, _ := json.Marshal(matches)
			hash := sha256.Sum256(data)
			s.ruleSets[tag] = map[string]any{"type": "remote", "tag": tag, "format": "source",
				"url": strings.TrimRight(subURL, "/") + "/karing-rules/" + name + "?v=" + hex.EncodeToString(hash[:8]), "download_detour": "direct"}
			s.order = append(s.order, tag)
			refs = append([]string{tag}, refs...)
		}
		for _, ref := range refs {
			rules = append(rules, map[string]any{"rule_set": ref, "outbound": target})
		}
	}
	block := r.Block
	if r.BlockAds {
		block = append([]string{"geosite:category-ads-all"}, block...)
	}
	add("block", "block", block)
	// Headless rule sets cannot match sniffed protocols. Do not turn a BitTorrent
	// policy into an overbroad rule for all traffic; Karing owns protocol detection.
	add("proxy", "proxy", r.Proxy)
	add("private", "direct", nil, map[string]any{"ip_cidr": []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.168.0.0/16", "::1/128", "fc00::/7", "fe80::/10",
	}})
	if r.RUDirect {
		add("ru", "direct", append(append([]string{}, ruDomains...), "geoip:ru"))
	}
	add("direct", "direct", r.Direct)
	add("final", "proxy", nil, map[string]any{"network": []string{"tcp", "udp"}})
	sets := make([]map[string]any, 0, len(s.order))
	for _, tag := range s.order {
		sets = append(sets, s.ruleSets[tag])
	}
	return map[string]any{"rule_set": sets, "rules": rules, "final": "proxy"}, sources
}

// KaringRuleSet is a source-format sing-box rule set. It contains matchers only, never server
// credentials; Karing fetches it without the subscription's HWID header.
func KaringRuleSet(name string, r service.ClientRouting) ([]byte, bool) {
	_, sources := karingRouting("", r)
	rules, ok := sources[name]
	if !ok {
		return nil, false
	}
	b, _ := json.Marshal(map[string]any{"version": 3, "rules": rules})
	return b, true
}
