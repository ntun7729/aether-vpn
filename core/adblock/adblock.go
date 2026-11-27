package adblock

import (
	"encoding/binary"
	"strings"
)

// DefaultBlockedSuffixes covers major ad, tracker, and telemetry networks
// (including standard adblock tester domains) with zero false positives for normal apps.
var DefaultBlockedSuffixes = []string{
	// Google Ads / Analytics / Telemetry
	"doubleclick.net",
	"googlesyndication.com",
	"googleadservices.com",
	"adservice.google.com",
	"pagead2.googlesyndication.com",
	"google-analytics.com",
	"analytics.google.com",
	"googletagmanager.com",
	"googletagservices.com",
	"app-measurement.com",
	"firebase-settings.crashlytics.com",
	"crashlytics.com",
	"admob.com",
	// Amazon / Microsoft / Yahoo / Yandex / Meta / TikTok / X / Pinterest / Reddit Ads
	"amazon-adsystem.com",
	"aax.amazon-adsystem.com",
	"ads.microsoft.com",
	"bat.bing.com",
	"clarity.ms",
	"ads.yahoo.com",
	"adtech.yahooinc.com",
	"analytics.yahoo.com",
	"mc.yandex.ru",
	"an.yandex.ru",
	"an.facebook.com",
	"pixel.facebook.com",
	"ads.facebook.com",
	"ads-twitter.com",
	"static.ads-twitter.com",
	"analytics.twitter.com",
	"ads.tiktok.com",
	"analytics.tiktok.com",
	"log-va.tiktokv.com",
	"mon.tiktokv.com",
	"ads.pinterest.com",
	"log.pinterest.com",
	"events.redditmedia.com",
	"ads.linkedin.com",
	"px.ads.linkedin.com",
	"tr.snapchat.com",
	"sc-static.net",
	// Programmatic Ad Networks & Exchanges
	"adnxs.com",
	"adsrvr.org",
	"adform.net",
	"adroll.com",
	"advertising.com",
	"adcolony.com",
	"adsterra.com",
	"adpushup.com",
	"bidswitch.net",
	"casalemedia.com",
	"contextweb.com",
	"criteo.com",
	"criteo.net",
	"doubleverify.com",
	"exoclick.com",
	"gumgum.com",
	"id5-sync.com",
	"indexww.com",
	"lijit.com",
	"media.net",
	"mgid.com",
	"moatads.com",
	"openx.net",
	"outbrain.com",
	"popads.net",
	"popcash.net",
	"propellerads.com",
	"pubmatic.com",
	"revcontent.com",
	"richaudience.com",
	"rubiconproject.com",
	"sharethrough.com",
	"smartadserver.com",
	"sonobi.com",
	"spotxchange.com",
	"taboola.com",
	"tapad.com",
	"teads.tv",
	"triplelift.com",
	"yieldmo.com",
	"zedo.com",
	"2mdn.net",
	"3lift.com",
	"adsafeprotected.com",
	// Mobile Ad SDKs & Attribution Trackers
	"unityads.unity3d.com",
	"applovin.com",
	"applvn.com",
	"vungle.com",
	"ironsrc.com",
	"supersonicads.com",
	"chartboost.com",
	"inmobi.com",
	"mopub.com",
	"tapjoy.com",
	"startappservice.com",
	"fyber.com",
	"liftoff.io",
	"pangle-global.com",
	"adjust.com",
	"appsflyer.com",
	"branch.io",
	"kochava.com",
	"singular.net",
	"tenjin.io",
	"flurry.com",
	// Web Analytics / Session Replay / Error Trackers
	"hotjar.com",
	"hotjar.io",
	"mixpanel.com",
	"amplitude.com",
	"segment.io",
	"segment.com",
	"scorecardresearch.com",
	"quantserve.com",
	"newrelic.com",
	"nr-data.net",
	"browser-intake-datadoghq.com",
	"sentry.io",
	"bugsnag.com",
	"luckyorange.com",
	"mouseflow.com",
	"fullstory.com",
	"crazyegg.com",
	"chartbeat.com",
	"omtrdc.net",
	"demdex.net",
	"bluekai.com",
	"krxd.net",
	"rlcdn.com",
	"turn.com",
	"mathtag.com",
}

// Blocker checks domains against a fast hash-set suffix blocklist.
type Blocker struct {
	enabled  bool
	suffixes map[string]struct{}
}

// New creates a Blocker. If enabled is false, IsBlocked always returns false.
func New(enabled bool, extra ...string) *Blocker {
	b := &Blocker{
		enabled:  enabled,
		suffixes: make(map[string]struct{}, len(DefaultBlockedSuffixes)+len(extra)),
	}
	if !enabled {
		return b
	}
	for _, s := range DefaultBlockedSuffixes {
		b.suffixes[strings.ToLower(strings.TrimSuffix(s, "."))] = struct{}{}
	}
	for _, s := range extra {
		s = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(s, ".")))
		if s != "" {
			b.suffixes[s] = struct{}{}
		}
	}
	return b
}

// Enabled reports whether ad blocking is active.
func (b *Blocker) Enabled() bool {
	return b != nil && b.enabled
}

// IsBlocked returns true if host matches or is a subdomain of a blocked domain.
func (b *Blocker) IsBlocked(host string) bool {
	if b == nil || !b.enabled || host == "" {
		return false
	}
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	for {
		if _, ok := b.suffixes[h]; ok {
			return true
		}
		idx := strings.IndexByte(h, '.')
		if idx < 0 {
			return false
		}
		h = h[idx+1:]
	}
}

// ExtractDNSQueryName parses the QNAME from a raw UDP DNS query packet.
func ExtractDNSQueryName(pkt []byte) (string, bool) {
	if len(pkt) < 13 {
		return "", false
	}
	if pkt[2]&0x80 != 0 || binary.BigEndian.Uint16(pkt[4:6]) == 0 {
		return "", false
	}
	pos := 12
	var parts []string
	for pos < len(pkt) {
		l := int(pkt[pos])
		if l == 0 {
			break
		}
		if l&0xc0 != 0 || pos+1+l > len(pkt) {
			return "", false
		}
		parts = append(parts, string(pkt[pos+1:pos+1+l]))
		pos += 1 + l
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "."), true
}

// BuildNXDomain crafts a minimal valid DNS NXDOMAIN response for a blocked query.
func BuildNXDomain(query []byte) []byte {
	if len(query) < 12 {
		return nil
	}
	resp := make([]byte, len(query))
	copy(resp, query)
	resp[2] = (query[2] & 0x01) | 0x81
	resp[3] = 0x83
	binary.BigEndian.PutUint16(resp[6:8], 0)
	binary.BigEndian.PutUint16(resp[8:10], 0)
	binary.BigEndian.PutUint16(resp[10:12], 0)
	return resp
}

// ExtractTLSSNI extracts the Server Name Indication (SNI) hostname from a raw TLS ClientHello record.
// Enables instant 0-latency ad/tracker blocking on Android TUN even when the client connects by IP!
func ExtractTLSSNI(data []byte) (string, bool) {
	// TLS Record Header: ContentType(1)=0x16 (Handshake), Version(2), Length(2)
	if len(data) < 43 || data[0] != 0x16 {
		return "", false
	}
	// Handshake Header: HandshakeType(1)=0x01 (ClientHello), Length(3), ClientVersion(2), Random(32)
	if data[5] != 0x01 {
		return "", false
	}
	pos := 5 + 1 + 3 + 2 + 32 // 43
	if pos >= len(data) {
		return "", false
	}
	// Session ID
	sessLen := int(data[pos])
	pos += 1 + sessLen
	if pos+2 > len(data) {
		return "", false
	}
	// Cipher Suites
	csLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
	pos += 2 + csLen
	if pos+1 > len(data) {
		return "", false
	}
	// Compression Methods
	compLen := int(data[pos])
	pos += 1 + compLen
	if pos+2 > len(data) {
		return "", false
	}
	// Extensions
	extTotalLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
	pos += 2
	extEnd := pos + extTotalLen
	if extEnd > len(data) {
		extEnd = len(data)
	}

	for pos+4 <= extEnd {
		extType := binary.BigEndian.Uint16(data[pos : pos+2])
		extLen := int(binary.BigEndian.Uint16(data[pos+2 : pos+4]))
		pos += 4
		if pos+extLen > extEnd {
			break
		}
		if extType == 0x0000 && extLen >= 5 { // server_name extension
			// ServerNameList length (2), NameType (1)=0x00 (host_name), HostName length (2)
			nameType := data[pos+2]
			nameLen := int(binary.BigEndian.Uint16(data[pos+3 : pos+5]))
			if nameType == 0x00 && 5+nameLen <= extLen {
				return string(data[pos+5 : pos+5+nameLen]), true
			}
		}
		pos += extLen
	}
	return "", false
}
