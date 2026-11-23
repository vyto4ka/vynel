// Package geostore manages Xray GeoSite and GeoIP (.dat) files: streaming protobuf indexing,
// on-demand category inspection by file offset, URL downloads with SSRF protection, and
// category export helpers.
package geostore

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
)

// Kind identifies whether a .dat file is a GeoSiteList or a GeoIPList.
type Kind string

const (
	KindGeoSite Kind = "geosite"
	KindGeoIP   Kind = "geoip"
)

// MaxEntryBytes bounds a single GeoSite or GeoIP protobuf entry during streaming reads.
const MaxEntryBytes = 32 << 20

// Xray router.Domain_Type values.
const (
	domainPlain      = 0
	domainRegex      = 1
	domainRootDomain = 2
	domainFull       = 3
)

// CategorySummary is the compact in-memory index entry for one category inside a .dat file.
type CategorySummary struct {
	Code       string   `json:"code"`
	Count      int      `json:"count"`
	Attributes []string `json:"attributes,omitempty"`
	Offset     int64    `json:"-"`
	Length     int64    `json:"-"`
}

// FileIndex holds the parsed table of contents of a single .dat file.
type FileIndex struct {
	Kind         Kind
	Categories   []CategorySummary
	ByCode       map[string]int // lowercase code -> index in Categories
	EntriesCount int
}

// DomainItem is one domain rule inside a GeoSite category.
type DomainItem struct {
	Type  string   `json:"type"`            // domain | full | keyword | regexp
	Value string   `json:"value"`           // raw value
	Attrs []string `json:"attrs,omitempty"` // attribute keys, e.g. ["cn", "ads"]
	Rule  string   `json:"rule"`            // Xray rule string, e.g. "domain:openai.com"
}

// IPItem is one CIDR entry inside a GeoIP category.
type IPItem struct {
	CIDR   string `json:"cidr"`
	Family int    `json:"family"` // 4 or 6
}

// CategoryDetails is the paginated inspection result for one category.
type CategoryDetails struct {
	File        string         `json:"file"`
	Kind        Kind           `json:"kind"`
	Code        string         `json:"code"`
	Total       int            `json:"total"`
	Filtered    int            `json:"filtered"`
	Offset      int            `json:"offset"`
	Limit       int            `json:"limit"`
	Attributes  map[string]int `json:"attributes,omitempty"`
	TypeCounts  map[string]int `json:"typeCounts,omitempty"`
	Domains     []DomainItem   `json:"domains,omitempty"`
	IPs         []IPItem       `json:"ips,omitempty"`
	FamilyCount map[string]int `json:"familyCount,omitempty"`
}

// ExpandedRules is the full set of rules in one category, exposed for both inspection and
// future client rule-set / rule-provider generation.
type ExpandedRules struct {
	Kind    Kind
	Code    string
	Domains []DomainItem
	CIDRs   []string
}

type rawDomain struct {
	typ   uint64
	value string
	attrs []string
}

type rawGeoSite struct {
	code    string
	domains []rawDomain
}

type rawCIDR struct {
	ip     []byte
	prefix uint32
}

type rawGeoIP struct {
	code  string
	cidrs []rawCIDR
}

func decodeGeoSite(b []byte) (rawGeoSite, error) {
	var gs rawGeoSite
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return gs, protowire.ParseError(n)
		}
		b = b[n:]
		switch {
		case num == 1 && typ == protowire.BytesType:
			s, m := protowire.ConsumeString(b)
			if m < 0 {
				return gs, protowire.ParseError(m)
			}
			gs.code = s
			b = b[m:]
		case num == 2 && typ == protowire.BytesType:
			raw, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return gs, protowire.ParseError(m)
			}
			d, err := decodeDomain(raw)
			if err != nil {
				return gs, err
			}
			gs.domains = append(gs.domains, d)
			b = b[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return gs, protowire.ParseError(m)
			}
			b = b[m:]
		}
	}
	return gs, nil
}

func decodeDomain(b []byte) (rawDomain, error) {
	var d rawDomain
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return d, protowire.ParseError(n)
		}
		b = b[n:]
		switch {
		case num == 1 && typ == protowire.VarintType:
			v, m := protowire.ConsumeVarint(b)
			if m < 0 {
				return d, protowire.ParseError(m)
			}
			d.typ = v
			b = b[m:]
		case num == 2 && typ == protowire.BytesType:
			s, m := protowire.ConsumeString(b)
			if m < 0 {
				return d, protowire.ParseError(m)
			}
			d.value = s
			b = b[m:]
		case num == 3 && typ == protowire.BytesType:
			raw, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return d, protowire.ParseError(m)
			}
			if key := decodeAttrKey(raw); key != "" {
				d.attrs = append(d.attrs, key)
			}
			b = b[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return d, protowire.ParseError(m)
			}
			b = b[m:]
		}
	}
	return d, nil
}

func decodeAttrKey(b []byte) string {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return ""
		}
		b = b[n:]
		if num == 1 && typ == protowire.BytesType {
			s, m := protowire.ConsumeString(b)
			if m < 0 {
				return ""
			}
			return strings.ToLower(strings.TrimSpace(s))
		}
		m := protowire.ConsumeFieldValue(num, typ, b)
		if m < 0 {
			return ""
		}
		b = b[m:]
	}
	return ""
}

func decodeGeoIP(b []byte) (rawGeoIP, error) {
	var gi rawGeoIP
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return gi, protowire.ParseError(n)
		}
		b = b[n:]
		switch {
		case num == 1 && typ == protowire.BytesType:
			s, m := protowire.ConsumeString(b)
			if m < 0 {
				return gi, protowire.ParseError(m)
			}
			gi.code = s
			b = b[m:]
		case num == 2 && typ == protowire.BytesType:
			raw, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return gi, protowire.ParseError(m)
			}
			c, err := decodeCIDR(raw)
			if err != nil {
				return gi, err
			}
			gi.cidrs = append(gi.cidrs, c)
			b = b[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return gi, protowire.ParseError(m)
			}
			b = b[m:]
		}
	}
	return gi, nil
}

func decodeCIDR(b []byte) (rawCIDR, error) {
	var c rawCIDR
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return c, protowire.ParseError(n)
		}
		b = b[n:]
		switch {
		case num == 1 && typ == protowire.BytesType:
			ip, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return c, protowire.ParseError(m)
			}
			c.ip = append([]byte(nil), ip...)
			b = b[m:]
		case num == 2 && typ == protowire.VarintType:
			v, m := protowire.ConsumeVarint(b)
			if m < 0 {
				return c, protowire.ParseError(m)
			}
			c.prefix = uint32(v)
			b = b[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return c, protowire.ParseError(m)
			}
			b = b[m:]
		}
	}
	return c, nil
}

// IndexFile streams a .dat file from disk and builds a compact category index without loading
// the entire protobuf list into memory. If expectedKind is empty, the kind is auto-detected.
func IndexFile(path string, expectedKind Kind) (*FileIndex, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() == 0 {
		return nil, errors.New("файл пустой")
	}

	// Sniff the first bytes to give clear diagnostics for common bad downloads (HTML, ZIP, JSON).
	head := make([]byte, min(int64(64), st.Size()))
	if _, err := io.ReadFull(f, head); err != nil {
		return nil, fmt.Errorf("чтение заголовка файла: %w", err)
	}
	if err := detectNonDat(head); err != nil {
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	br := bufio.NewReaderSize(f, 64*1024)
	var pos int64
	kind := expectedKind
	idx := &FileIndex{ByCode: map[string]int{}}

	for pos < st.Size() {
		tagVal, n, err := readVarint(br)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("повреждённый protobuf (смещение %d): %w", pos, err)
		}
		pos += int64(n)
		fieldNum, wireType := protowire.DecodeTag(tagVal)
		if fieldNum <= 0 {
			return nil, fmt.Errorf("некорректный тег protobuf на смещении %d", pos)
		}
		if wireType != protowire.BytesType {
			skipped, err := skipWireField(br, wireType)
			if err != nil {
				return nil, fmt.Errorf("повреждённый protobuf (смещение %d): %w", pos, err)
			}
			pos += skipped
			continue
		}
		length, n, err := readVarint(br)
		if err != nil {
			return nil, fmt.Errorf("повреждённая длина записи protobuf (смещение %d): %w", pos, err)
		}
		pos += int64(n)
		entryLen := int64(length)
		if entryLen < 0 || entryLen > MaxEntryBytes || pos+entryLen > st.Size() {
			return nil, fmt.Errorf("некорректная длина записи protobuf (%d байт)", entryLen)
		}
		entryOffset := pos
		buf := make([]byte, entryLen)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, fmt.Errorf("неполная запись protobuf: %w", err)
		}
		pos += entryLen

		if fieldNum != 1 {
			continue
		}

		if kind == "" {
			detected, err := detectEntryKind(buf)
			if err != nil {
				continue
			}
			kind = detected
		}

		switch kind {
		case KindGeoSite:
			gs, err := decodeGeoSite(buf)
			if err != nil {
				return nil, fmt.Errorf("не удалось прочитать запись GeoSite: %w", err)
			}
			code := strings.ToLower(strings.TrimSpace(gs.code))
			if code == "" {
				continue
			}
			if !looksLikeGeoSite(&gs) && len(gs.domains) > 0 {
				return nil, errors.New("файл не является корректной базой GeoSite (.dat)")
			}
			attrs := collectAttrs(&gs)
			idx.EntriesCount += len(gs.domains)
			if prev, exists := idx.ByCode[code]; exists {
				idx.Categories[prev].Count += len(gs.domains)
				idx.Categories[prev].Attributes = mergeUniqueSorted(idx.Categories[prev].Attributes, attrs)
			} else {
				idx.ByCode[code] = len(idx.Categories)
				idx.Categories = append(idx.Categories, CategorySummary{
					Code:       code,
					Count:      len(gs.domains),
					Attributes: attrs,
					Offset:     entryOffset,
					Length:     entryLen,
				})
			}
		case KindGeoIP:
			gi, err := decodeGeoIP(buf)
			if err != nil {
				return nil, fmt.Errorf("не удалось прочитать запись GeoIP: %w", err)
			}
			code := strings.ToLower(strings.TrimSpace(gi.code))
			if code == "" {
				continue
			}
			if !looksLikeGeoIP(&gi) && len(gi.cidrs) > 0 {
				return nil, errors.New("файл не является корректной базой GeoIP (.dat)")
			}
			idx.EntriesCount += len(gi.cidrs)
			if prev, exists := idx.ByCode[code]; exists {
				idx.Categories[prev].Count += len(gi.cidrs)
			} else {
				idx.ByCode[code] = len(idx.Categories)
				idx.Categories = append(idx.Categories, CategorySummary{
					Code:   code,
					Count:  len(gi.cidrs),
					Offset: entryOffset,
					Length: entryLen,
				})
			}
		default:
			return nil, fmt.Errorf("неизвестный тип geo-файла: %q", kind)
		}
	}

	if kind == "" || len(idx.Categories) == 0 {
		return nil, errors.New("файл не содержит категорий GeoSite или GeoIP")
	}
	idx.Kind = kind
	sort.Slice(idx.Categories, func(i, j int) bool { return idx.Categories[i].Code < idx.Categories[j].Code })
	idx.ByCode = make(map[string]int, len(idx.Categories))
	for i, c := range idx.Categories {
		idx.ByCode[c.Code] = i
	}
	return idx, nil
}

// ExportCategory reads one category directly from disk by its indexed offset and returns all rules.
func ExportCategory(path string, idx *FileIndex, code string) (*ExpandedRules, error) {
	if idx == nil {
		return nil, errors.New("индекс файла не загружен")
	}
	code = strings.ToLower(strings.TrimSpace(code))
	pos, ok := idx.ByCode[code]
	if !ok {
		return nil, fmt.Errorf("категория %q не найдена", code)
	}
	sum := idx.Categories[pos]
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := make([]byte, sum.Length)
	if _, err := f.ReadAt(buf, sum.Offset); err != nil {
		return nil, fmt.Errorf("чтение категории %q: %w", code, err)
	}

	out := &ExpandedRules{Kind: idx.Kind, Code: code}
	switch idx.Kind {
	case KindGeoSite:
		gs, err := decodeGeoSite(buf)
		if err != nil {
			return nil, fmt.Errorf("декодирование GeoSite %q: %w", code, err)
		}
		out.Domains = make([]DomainItem, 0, len(gs.domains))
		for _, d := range gs.domains {
			out.Domains = append(out.Domains, toDomainItem(d))
		}
	case KindGeoIP:
		gi, err := decodeGeoIP(buf)
		if err != nil {
			return nil, fmt.Errorf("декодирование GeoIP %q: %w", code, err)
		}
		out.CIDRs = make([]string, 0, len(gi.cidrs))
		for _, c := range gi.cidrs {
			if s := formatCIDR(c); s != "" {
				out.CIDRs = append(out.CIDRs, s)
			}
		}
	default:
		return nil, fmt.Errorf("неизвестный тип %q", idx.Kind)
	}
	return out, nil
}

// InspectCategory returns paginated and filtered items for one category.
func InspectCategory(fileName, path string, idx *FileIndex, code, query, attr string, offset, limit int) (*CategoryDetails, error) {
	exp, err := ExportCategory(path, idx, code)
	if err != nil {
		return nil, err
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	q := strings.ToLower(strings.TrimSpace(query))
	attr = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(attr), "@"))

	det := &CategoryDetails{
		File:   fileName,
		Kind:   exp.Kind,
		Code:   exp.Code,
		Offset: offset,
		Limit:  limit,
	}

	switch exp.Kind {
	case KindGeoSite:
		det.Total = len(exp.Domains)
		det.Attributes = map[string]int{}
		det.TypeCounts = map[string]int{}
		filtered := make([]DomainItem, 0, len(exp.Domains))
		for _, d := range exp.Domains {
			det.TypeCounts[d.Type]++
			for _, a := range d.Attrs {
				det.Attributes[a]++
			}
			if attr != "" && !slices.Contains(d.Attrs, attr) {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(d.Value), q) && !strings.Contains(strings.ToLower(d.Rule), q) {
				continue
			}
			filtered = append(filtered, d)
		}
		det.Filtered = len(filtered)
		if offset < len(filtered) {
			end := min(offset+limit, len(filtered))
			det.Domains = filtered[offset:end]
		} else {
			det.Domains = []DomainItem{}
		}
	case KindGeoIP:
		det.Total = len(exp.CIDRs)
		det.FamilyCount = map[string]int{"4": 0, "6": 0}
		var searchIP netip.Addr
		hasIP := false
		if q != "" {
			if addr, err := netip.ParseAddr(q); err == nil {
				searchIP = addr
				hasIP = true
			}
		}
		filtered := make([]IPItem, 0, len(exp.CIDRs))
		for _, cidr := range exp.CIDRs {
			fam := 4
			if strings.Contains(cidr, ":") {
				fam = 6
				det.FamilyCount["6"]++
			} else {
				det.FamilyCount["4"]++
			}
			if q != "" {
				matched := strings.Contains(strings.ToLower(cidr), q)
				if !matched && hasIP {
					if pfx, err := netip.ParsePrefix(cidr); err == nil && pfx.Contains(searchIP) {
						matched = true
					}
				}
				if !matched {
					continue
				}
			}
			filtered = append(filtered, IPItem{CIDR: cidr, Family: fam})
		}
		det.Filtered = len(filtered)
		if offset < len(filtered) {
			end := min(offset+limit, len(filtered))
			det.IPs = filtered[offset:end]
		} else {
			det.IPs = []IPItem{}
		}
	}
	return det, nil
}

// SearchContent scans categories in a .dat file and returns those that match the given domain or IP.
func SearchContent(path string, idx *FileIndex, query string, limit int) ([]CategorySummary, error) {
	if idx == nil {
		return nil, nil
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}

	var queryAddr netip.Addr
	isIP := false
	if addr, err := netip.ParseAddr(q); err == nil {
		queryAddr = addr
		isIP = true
	}

	if idx.Kind == KindGeoIP && !isIP {
		if !strings.ContainsAny(q, "0123456789abcdef.:/") {
			return nil, nil
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var matched []CategorySummary
	for _, c := range idx.Categories {
		if len(matched) >= limit {
			break
		}
		buf := make([]byte, c.Length)
		if _, err := f.ReadAt(buf, c.Offset); err != nil {
			continue
		}
		switch idx.Kind {
		case KindGeoSite:
			gs, err := decodeGeoSite(buf)
			if err != nil {
				continue
			}
			if geoSiteMatches(&gs, q) {
				matched = append(matched, c)
			}
		case KindGeoIP:
			gi, err := decodeGeoIP(buf)
			if err != nil {
				continue
			}
			if geoIPMatches(&gi, q, queryAddr, isIP) {
				matched = append(matched, c)
			}
		}
	}
	return matched, nil
}

func geoSiteMatches(gs *rawGeoSite, q string) bool {
	for _, d := range gs.domains {
		val := strings.ToLower(d.value)
		switch d.typ {
		case domainFull:
			if val == q || strings.Contains(val, q) {
				return true
			}
		case domainRootDomain:
			if val == q || strings.HasSuffix(q, "."+val) || strings.Contains(val, q) {
				return true
			}
		case domainPlain:
			if strings.Contains(q, val) || strings.Contains(val, q) {
				return true
			}
		case domainRegex:
			if strings.Contains(val, q) {
				return true
			}
			if re, err := regexp.Compile(d.value); err == nil && re.MatchString(q) {
				return true
			}
		}
	}
	return false
}

func geoIPMatches(gi *rawGeoIP, q string, addr netip.Addr, isIP bool) bool {
	for _, c := range gi.cidrs {
		if isIP {
			if ipAddr, ok := netip.AddrFromSlice(c.ip); ok {
				pfx := netip.PrefixFrom(ipAddr, int(c.prefix))
				if pfx.IsValid() && pfx.Contains(addr) {
					return true
				}
			}
		} else {
			if s := formatCIDR(c); strings.Contains(strings.ToLower(s), q) {
				return true
			}
		}
	}
	return false
}

func detectNonDat(head []byte) error {
	trimmed := bytes.TrimSpace(head)
	if len(trimmed) >= 4 && trimmed[0] == 'P' && trimmed[1] == 'K' && trimmed[2] == 0x03 && trimmed[3] == 0x04 {
		return errors.New("по ссылке скачался архив ZIP, укажите прямую ссылку на .dat файл")
	}
	if len(trimmed) >= 2 && trimmed[0] == 0x1f && trimmed[1] == 0x8b {
		return errors.New("по ссылке скачался архив GZIP, укажите прямую ссылку на .dat файл")
	}
	low := strings.ToLower(string(trimmed))
	if strings.HasPrefix(low, "<!doctype") || strings.HasPrefix(low, "<html") || strings.HasPrefix(low, "<?xml") {
		return errors.New("по ссылке скачалась HTML-страница вместо .dat файла")
	}
	if strings.HasPrefix(low, "{") || strings.HasPrefix(low, "[") {
		return errors.New("по ссылке скачался JSON вместо бинарного .dat файла")
	}
	return nil
}

func detectEntryKind(buf []byte) (Kind, error) {
	if gs, err := decodeGeoSite(buf); err == nil && gs.code != "" && len(gs.domains) > 0 && looksLikeGeoSite(&gs) {
		return KindGeoSite, nil
	}
	if gi, err := decodeGeoIP(buf); err == nil && gi.code != "" && len(gi.cidrs) > 0 && looksLikeGeoIP(&gi) {
		return KindGeoIP, nil
	}
	return "", errors.New("не удалось определить тип записи")
}

func looksLikeGeoSite(gs *rawGeoSite) bool {
	checked := 0
	for _, d := range gs.domains {
		if d.value == "" || d.typ > domainFull {
			return false
		}
		checked++
		if checked >= 16 {
			break
		}
	}
	return checked > 0
}

func looksLikeGeoIP(gi *rawGeoIP) bool {
	checked := 0
	for _, c := range gi.cidrs {
		switch len(c.ip) {
		case 4:
			if c.prefix > 32 {
				return false
			}
		case 16:
			if c.prefix > 128 {
				return false
			}
		default:
			return false
		}
		checked++
		if checked >= 16 {
			break
		}
	}
	return checked > 0
}

func collectAttrs(gs *rawGeoSite) []string {
	set := map[string]struct{}{}
	for _, d := range gs.domains {
		for _, k := range d.attrs {
			if k != "" {
				set[k] = struct{}{}
			}
		}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func mergeUniqueSorted(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	set := make(map[string]struct{}, len(a)+len(b))
	for _, x := range a {
		set[x] = struct{}{}
	}
	for _, x := range b {
		set[x] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for x := range set {
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}

func toDomainItem(d rawDomain) DomainItem {
	var typ, prefix string
	switch d.typ {
	case domainRootDomain:
		typ, prefix = "domain", "domain:"
	case domainFull:
		typ, prefix = "full", "full:"
	case domainRegex:
		typ, prefix = "regexp", "regexp:"
	default:
		typ, prefix = "keyword", "keyword:"
	}
	var attrs []string
	for _, k := range d.attrs {
		if k != "" && !slices.Contains(attrs, k) {
			attrs = append(attrs, k)
		}
	}
	sort.Strings(attrs)
	return DomainItem{
		Type:  typ,
		Value: d.value,
		Attrs: attrs,
		Rule:  prefix + d.value,
	}
}

func formatCIDR(c rawCIDR) string {
	ip := net.IP(c.ip)
	if len(c.ip) == 4 {
		ip = ip.To4()
	}
	if ip == nil {
		return ""
	}
	return fmt.Sprintf("%s/%d", ip.String(), c.prefix)
}

func readVarint(br *bufio.Reader) (uint64, int, error) {
	var x uint64
	var s uint
	for i := 0; i < binary.MaxVarintLen64; i++ {
		b, err := br.ReadByte()
		if err != nil {
			if i == 0 && errors.Is(err, io.EOF) {
				return 0, 0, io.EOF
			}
			return 0, i, err
		}
		if b < 0x80 {
			if i == binary.MaxVarintLen64-1 && b > 1 {
				return 0, i + 1, errors.New("varint overflow")
			}
			return x | uint64(b)<<s, i + 1, nil
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
	return 0, binary.MaxVarintLen64, errors.New("varint overflow")
}

func skipWireField(br *bufio.Reader, wireType protowire.Type) (int64, error) {
	switch wireType {
	case protowire.VarintType:
		_, n, err := readVarint(br)
		return int64(n), err
	case protowire.Fixed32Type:
		n, err := io.CopyN(io.Discard, br, 4)
		return n, err
	case protowire.Fixed64Type:
		n, err := io.CopyN(io.Discard, br, 8)
		return n, err
	case protowire.BytesType:
		l, n, err := readVarint(br)
		if err != nil {
			return int64(n), err
		}
		skipped, err := io.CopyN(io.Discard, br, int64(l))
		return int64(n) + skipped, err
	default:
		return 0, fmt.Errorf("unsupported wire type %d", wireType)
	}
}
