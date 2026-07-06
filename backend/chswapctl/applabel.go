package main

// applabel.go — resolve Android app display names (labels) for the per-app
// view, using only the standard library (no third-party deps).
//
// Android does not expose a command that returns a localized app label, so we
// do what the framework's AssetManager does: read the APK, parse the binary
// AndroidManifest.xml for <application android:label=...>, and if it is a
// resource reference resolve it from resources.arsc (preferring zh-CN). Results
// are cached to data/label-cache.json keyed by package name; parsing is bounded
// per call and driven incrementally by the WebUI's polling.

import (
	"archive/zip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// Per /v1/apps-call parsing bounds. Labels not resolved within the budget are
// left pending and picked up on the next poll; once cached they are instant.
const (
	labelBudget      = 1500 * time.Millisecond
	labelConcurrency = 4
)

// Chunk type ids from the resources / binary-XML format.
const (
	chunkStringPool  = 0x0001
	chunkTableType   = 0x0201 // ResTable_type (has config + entries)
	chunkTablePkg    = 0x0200
	chunkXMLStartEl  = 0x0102
	resTypeReference = 0x01
	resTypeString    = 0x03
	entryFlagComplex = 0x0001
	utf8Flag         = 1 << 8
)

// builtinAppNames is a FALLBACK ONLY: used when an APK's label cannot be parsed
// (e.g. framework/system packages whose label references framework resources).
// APK-parsed labels are always authoritative — never let this table override a
// successfully parsed name (real ROM labels vary, e.g. heytap market is "软件商店").
var builtinAppNames = map[string]string{
	"android":                            "Android 系统",
	"com.android.settings":               "设置",
	"com.android.systemui":               "系统界面",
	"com.android.phone":                  "电话服务",
	"com.tencent.mm":                     "微信",
	"com.tencent.mobileqq":               "QQ",
	"com.eg.android.AlipayGphone":        "支付宝",
	"com.taobao.taobao":                  "淘宝",
	"com.jingdong.app.mall":              "京东",
	"com.sankuai.meituan":                "美团",
	"com.ss.android.ugc.aweme":           "抖音",
	"com.smile.gifmaker":                 "快手",
	"tv.danmaku.bili":                    "哔哩哔哩",
	"com.sina.weibo":                     "微博",
	"com.netease.cloudmusic":             "网易云音乐",
	"com.tencent.qqmusic":                "QQ音乐",
	"com.xingin.xhs":                     "小红书",
	"com.baidu.searchbox":                "百度",
	"com.zhihu.android":                  "知乎",
	"com.tencent.mobileqqi":              "QQ",
	"com.tencent.wework":                 "企业微信",
	"com.alibaba.android.rimet":          "钉钉",
	"com.autonavi.minimap":               "高德地图",
	"com.baidu.BaiduMap":                 "百度地图",
	"com.UCMobile":                       "UC浏览器",
	"com.tencent.mtt":                    "QQ浏览器",
	"com.android.chrome":                 "Chrome",
	"com.google.android.youtube":         "YouTube",
	"com.android.vending":                "Google Play 商店",
	"com.google.android.gms":             "Google Play 服务",
}

// ---- label cache (data/label-cache.json) ----

type labelCacheEntry struct {
	Label string `json:"label"`           // resolved name ("" = attempted, unresolved)
	APK   string `json:"apk,omitempty"`   // apk path at resolve time
	Size  int64  `json:"size,omitempty"`  // apk size (for future staleness checks)
	MTime int64  `json:"mtime,omitempty"` // apk mtime ns
}

func (a *App) labelCachePath() string {
	return filepath.Join(a.DataDir, "label-cache.json")
}

func (a *App) loadLabelCache() map[string]labelCacheEntry {
	m := map[string]labelCacheEntry{}
	data, err := os.ReadFile(a.labelCachePath())
	if err != nil {
		return m
	}
	_ = json.Unmarshal(data, &m)
	if m == nil {
		m = map[string]labelCacheEntry{}
	}
	return m
}

func (a *App) saveLabelCache(m map[string]labelCacheEntry) {
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	_ = atomicWrite(a.labelCachePath(), data, 0600)
}

// packageApkPaths returns package -> base.apk path via a single `pm list
// packages -f`, so we don't fork pm once per app. Empty on non-Android hosts.
func (a *App) packageApkPaths() map[string]string {
	out := map[string]string{}
	res, err := a.runCommandOut(3*time.Second, "pm", "list", "packages", "-f")
	if err != nil || strings.TrimSpace(res) == "" {
		res, err = a.runCommandOut(3*time.Second, "cmd", "package", "list", "packages", "-f")
		if err != nil {
			return out
		}
	}
	for _, line := range strings.Split(res, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "package:") {
			continue
		}
		line = strings.TrimPrefix(line, "package:")
		// format: /path/base.apk=com.pkg.name
		eq := strings.LastIndex(line, "=")
		if eq <= 0 {
			continue
		}
		apk, pkg := line[:eq], line[eq+1:]
		if pkg != "" && apk != "" {
			out[pkg] = apk
		}
	}
	return out
}

// fillAppLabels fills Label on apps from builtin table + cache, then parses a
// bounded, concurrent batch of the remaining APKs (writing them to cache).
// Returns how many apps still have no attempted label (frontend "解析中").
func (a *App) fillAppLabels(apps []AppMemInfo) int {
	cache := a.loadLabelCache()

	type job struct {
		idx  int
		name string
	}
	var jobs []job
	for i := range apps {
		n := apps[i].Name
		if n == "" {
			continue
		}
		if e, ok := cache[n]; ok {
			apps[i].Label = pickLabel(e.Label, n)
			continue // already attempted; not pending
		}
		jobs = append(jobs, job{i, n})
	}
	if len(jobs) == 0 {
		return 0
	}

	paths := a.packageApkPaths()
	deadline := time.Now().Add(labelBudget)
	sem := make(chan struct{}, labelConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	dirty := false
	pending := 0

	for _, jb := range jobs {
		if time.Now().After(deadline) {
			pending++
			continue
		}
		apk := paths[jb.name]
		wg.Add(1)
		sem <- struct{}{}
		go func(jb job, apk string) {
			defer wg.Done()
			defer func() { <-sem }()
			entry := labelCacheEntry{APK: apk}
			if apk != "" {
				if fi, err := os.Stat(apk); err == nil {
					entry.Size = fi.Size()
					entry.MTime = fi.ModTime().UnixNano()
				}
				if s, err := parseApkLabel(apk); err == nil {
					entry.Label = s
				}
			}
			mu.Lock()
			cache[jb.name] = entry
			apps[jb.idx].Label = pickLabel(entry.Label, jb.name)
			dirty = true
			mu.Unlock()
		}(jb, apk)
	}
	wg.Wait()
	if dirty {
		a.saveLabelCache(cache)
	}
	return pending
}

// pickLabel prefers the APK-parsed label; falls back to the builtin table only
// when parsing produced nothing (empty = attempted but unresolved).
func pickLabel(parsed, name string) string {
	if parsed != "" {
		return parsed
	}
	return builtinAppNames[name]
}

// ---- APK label parsing (pure stdlib) ----

func readZipFile(f *zip.File) []byte {
	rc, err := f.Open()
	if err != nil {
		return nil
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil
	}
	return data
}

// parseApkLabel returns the localized application label for an APK: read the
// binary manifest for <application android:label>, resolving a resource
// reference against resources.arsc (zh-CN preferred) when needed.
func parseApkLabel(apk string) (label string, err error) {
	defer func() {
		if r := recover(); r != nil {
			label, err = "", fmt.Errorf("apk parse panic: %v", r)
		}
	}()
	zr, err := zip.OpenReader(apk)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	var manifest, arsc []byte
	for _, f := range zr.File {
		switch f.Name {
		case "AndroidManifest.xml":
			manifest = readZipFile(f)
		case "resources.arsc":
			arsc = readZipFile(f)
		}
	}
	if manifest == nil {
		return "", errors.New("no AndroidManifest.xml")
	}
	lit, resID, ok := axmlAppLabel(manifest)
	if !ok {
		return "", errors.New("no application label")
	}
	if lit != "" {
		return strings.TrimSpace(lit), nil
	}
	if arsc == nil {
		return "", errors.New("no resources.arsc")
	}
	s, ok := arscString(arsc, resID)
	if !ok {
		return "", errors.New("label reference unresolved")
	}
	return strings.TrimSpace(s), nil
}

// stringPool decodes strings on demand from a RES_STRING_POOL chunk.
type stringPool struct {
	data    []byte
	count   int
	utf8    bool
	offBase int
	strBase int
}

func parsePool(b []byte, at int) *stringPool {
	return &stringPool{
		data:    b,
		count:   rd32(b, at+8),
		utf8:    rd32(b, at+16)&utf8Flag != 0,
		offBase: at + rd16(b, at+2),
		strBase: at + rd32(b, at+20),
	}
}

func (p *stringPool) str(i int) string {
	if p == nil || i < 0 || i >= p.count {
		return ""
	}
	q := p.strBase + rd32(p.data, p.offBase+i*4)
	b := p.data
	if p.utf8 {
		_, n1 := decLen8(b, q)
		q += n1
		l, n2 := decLen8(b, q)
		q += n2
		if q < 0 || q+l > len(b) {
			return ""
		}
		return string(b[q : q+l])
	}
	l, n := decLen16(b, q)
	q += n
	if q < 0 || q+l*2 > len(b) {
		return ""
	}
	u := make([]uint16, l)
	for j := 0; j < l; j++ {
		u[j] = binary.LittleEndian.Uint16(b[q+j*2:])
	}
	return string(utf16.Decode(u))
}

// axmlAppLabel finds <application android:label>. Returns (literal,"",true) for
// a string value, ("",resID,true) for a reference, or ("",0,false) if absent.
func axmlAppLabel(b []byte) (lit string, resID uint32, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			lit, resID, ok = "", 0, false
		}
	}()
	if len(b) < 8 {
		return "", 0, false
	}
	var pool *stringPool
	pos := 8
	for pos+8 <= len(b) {
		ctype := rd16(b, pos)
		csize := rd32(b, pos+4)
		if csize <= 0 || pos+csize > len(b) {
			break
		}
		switch ctype {
		case chunkStringPool:
			if pool == nil {
				pool = parsePool(b, pos)
			}
		case chunkXMLStartEl:
			if pool != nil && pool.str(rd32(b, pos+20)) == "application" {
				attrStart := rd16(b, pos+24)
				attrSize := rd16(b, pos+26)
				attrCount := rd16(b, pos+28)
				base := pos + 16 + attrStart
				for i := 0; i < attrCount; i++ {
					ap := base + i*attrSize
					if ap+20 > len(b) {
						break
					}
					if pool.str(rd32(b, ap+4)) != "label" {
						continue
					}
					dataType := b[ap+15]
					data := rd32(b, ap+16)
					if dataType == resTypeString {
						return pool.str(data), 0, true
					}
					if dataType == resTypeReference {
						return "", uint32(data), true
					}
				}
			}
		}
		pos += csize
	}
	return "", 0, false
}

// arscString resolves a string resource id from resources.arsc, choosing the
// best config (zh-CN > zh > default > any).
func arscString(b []byte, resID uint32) (out string, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			out, ok = "", false
		}
	}()
	if resID == 0 || len(b) < 12 {
		return "", false
	}
	pkgID := int(resID >> 24)
	typeID := int((resID >> 16) & 0xff)
	entryID := int(resID & 0xffff)

	global := parsePool(b, rd16(b, 2)) // global value pool follows table header
	best := -1
	bestStr := ""

	pos := rd16(b, 2) + rd32(b, rd16(b, 2)+4) // after table header + global pool
	for pos+8 <= len(b) {
		ctype := rd16(b, pos)
		csize := rd32(b, pos+4)
		if csize <= 0 || pos+csize > len(b) {
			break
		}
		if ctype == chunkTablePkg && rd32(b, pos+8) == pkgID {
			scanPackage(b, pos, csize, typeID, entryID, global, &best, &bestStr)
		}
		pos += csize
	}
	if best >= 0 {
		return bestStr, true
	}
	return "", false
}

func scanPackage(b []byte, pkgStart, pkgSize, typeID, entryID int, global *stringPool, best *int, bestStr *string) {
	pos := pkgStart + rd16(b, pkgStart+2)
	end := pkgStart + pkgSize
	for pos+8 <= end {
		ctype := rd16(b, pos)
		csize := rd32(b, pos+4)
		if csize <= 0 || pos+csize > end {
			break
		}
		if ctype == chunkTableType && int(b[pos+8]) == typeID {
			entryCount := rd32(b, pos+12)
			entriesStart := rd32(b, pos+16)
			if entryID < entryCount {
				offPos := pos + rd16(b, pos+2) + entryID*4
				if offPos+4 <= pos+csize {
					off := rd32(b, offPos)
					if off != 0xffffffff {
						if s, ok := readEntryString(b, pos+entriesStart+off, global); ok {
							if sc := configScore(b, pos+20); sc > *best {
								*best, *bestStr = sc, s
							}
						}
					}
				}
			}
		}
		pos += csize
	}
}

func readEntryString(b []byte, ep int, global *stringPool) (string, bool) {
	if ep < 0 || ep+8 > len(b) {
		return "", false
	}
	if rd16(b, ep+2)&entryFlagComplex != 0 {
		return "", false // bag/map, not a simple string value
	}
	vp := ep + rd16(b, ep) // Res_value follows the ResTable_entry header
	if vp+8 > len(b) {
		return "", false
	}
	if b[vp+3] != resTypeString {
		return "", false
	}
	return global.str(rd32(b, vp+4)), true
}

// configScore ranks a ResTable_config for a Simplified-Chinese (zh-CN) device.
// Crucially, Traditional-Chinese regions (zh-TW/HK/MO) must rank BELOW the
// default/base resource: an app like JD defines default="京东"(simplified),
// en="JD.COM", zh-HK="京東"(traditional) and no zh-CN — the simplified base is
// the right pick, not the Hong Kong traditional one.
// ResTable_config: size(u32), mcc/mnc(u16 each), language[2] @+8, country[2] @+10.
func configScore(b []byte, cs int) int {
	if cs+12 > len(b) {
		return 3 // too short to read a locale — treat as base/default
	}
	lang0, lang1 := b[cs+8], b[cs+9]
	country0, country1 := b[cs+10], b[cs+11]
	if lang0 == 'z' && lang1 == 'h' {
		switch {
		case country0 == 'C' && country1 == 'N':
			return 5 // zh-CN: exact simplified match
		case country0 == 0 && country1 == 0:
			return 4 // generic zh (no region): treated as simplified
		case country0 == 'T' && country1 == 'W',
			country0 == 'H' && country1 == 'K',
			country0 == 'M' && country1 == 'O':
			return 1 // Traditional Chinese: avoid — rank below the base resource
		default:
			return 2 // other zh region
		}
	}
	if lang0 == 0 && lang1 == 0 {
		return 3 // default/base resource (usually the app's primary language)
	}
	return 0 // other language: lowest, still a last-resort fallback
}

func decLen8(b []byte, p int) (int, int) {
	x := int(b[p])
	if x&0x80 != 0 {
		return (x&0x7f)<<8 | int(b[p+1]), 2
	}
	return x, 1
}

func decLen16(b []byte, p int) (int, int) {
	x := int(binary.LittleEndian.Uint16(b[p:]))
	if x&0x8000 != 0 {
		return (x&0x7fff)<<16 | int(binary.LittleEndian.Uint16(b[p+2:])), 4
	}
	return x, 2
}

func rd16(b []byte, o int) int { return int(binary.LittleEndian.Uint16(b[o:])) }
func rd32(b []byte, o int) int { return int(binary.LittleEndian.Uint32(b[o:])) }
