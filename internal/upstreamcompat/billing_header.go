package upstreamcompat

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"regexp"
)

var (
	claudeCodeUAVersionPattern = regexp.MustCompile(`(?i)^claude-cli/(\d+\.\d+\.\d+)`)
	ccVersionInBillingRe      = regexp.MustCompile(`(x-anthropic-billing-header:[^"]*?\bcc_version=)\d+\.\d+\.\d+`)
	cchPlaceholderRe          = regexp.MustCompile(`(x-anthropic-billing-header:[^"]*?\bcch=)(00000)(;)`)
)

const cchSeed uint64 = 0x6E52736AC806831E

func normalizeAnthropicBillingHeader(body []byte, userAgent string) []byte {
	body = syncBillingHeaderVersion(body, userAgent)
	return signBillingHeaderCCH(body)
}

func syncBillingHeaderVersion(body []byte, userAgent string) []byte {
	version := extractClaudeCodeVersion(userAgent)
	if version == "" {
		return body
	}
	replacement := []byte("${1}" + version)
	return ccVersionInBillingRe.ReplaceAll(body, replacement)
}

func extractClaudeCodeVersion(userAgent string) string {
	matches := claudeCodeUAVersionPattern.FindStringSubmatch(userAgent)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

func signBillingHeaderCCH(body []byte) []byte {
	if !cchPlaceholderRe.Match(body) {
		return body
	}
	cch := fmt.Sprintf("%05x", xxHash64Seeded(body, cchSeed)&0xFFFFF)
	return cchPlaceholderRe.ReplaceAll(body, []byte("${1}"+cch+"${3}"))
}

func xxHash64Seeded(data []byte, seed uint64) uint64 {
	const (
		prime1 uint64 = 11400714785074694791
		prime2 uint64 = 14029467366897019727
		prime3 uint64 = 1609587929392839161
		prime4 uint64 = 9650029242287828579
		prime5 uint64 = 2870177450012600261
	)
	round := func(acc uint64, input uint64) uint64 {
		acc += input * prime2
		acc = bits.RotateLeft64(acc, 31)
		acc *= prime1
		return acc
	}
	mergeRound := func(acc uint64, value uint64) uint64 {
		acc ^= round(0, value)
		acc = acc*prime1 + prime4
		return acc
	}

	remaining := data
	var h uint64
	if len(remaining) >= 32 {
		v1 := seed + prime1 + prime2
		v2 := seed + prime2
		v3 := seed
		v4 := seed - prime1
		for len(remaining) >= 32 {
			v1 = round(v1, binary.LittleEndian.Uint64(remaining[0:8]))
			v2 = round(v2, binary.LittleEndian.Uint64(remaining[8:16]))
			v3 = round(v3, binary.LittleEndian.Uint64(remaining[16:24]))
			v4 = round(v4, binary.LittleEndian.Uint64(remaining[24:32]))
			remaining = remaining[32:]
		}
		h = bits.RotateLeft64(v1, 1) + bits.RotateLeft64(v2, 7) + bits.RotateLeft64(v3, 12) + bits.RotateLeft64(v4, 18)
		h = mergeRound(h, v1)
		h = mergeRound(h, v2)
		h = mergeRound(h, v3)
		h = mergeRound(h, v4)
	} else {
		h = seed + prime5
	}

	h += uint64(len(data))
	for len(remaining) >= 8 {
		lane := binary.LittleEndian.Uint64(remaining[:8])
		h ^= round(0, lane)
		h = bits.RotateLeft64(h, 27)*prime1 + prime4
		remaining = remaining[8:]
	}
	if len(remaining) >= 4 {
		h ^= uint64(binary.LittleEndian.Uint32(remaining[:4])) * prime1
		h = bits.RotateLeft64(h, 23)*prime2 + prime3
		remaining = remaining[4:]
	}
	for _, b := range remaining {
		h ^= uint64(b) * prime5
		h = bits.RotateLeft64(h, 11) * prime1
	}

	h ^= h >> 33
	h *= prime2
	h ^= h >> 29
	h *= prime3
	h ^= h >> 32
	return h
}
