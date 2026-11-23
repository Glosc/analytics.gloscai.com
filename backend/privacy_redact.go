package main

import (
	"net/netip"
	"regexp"
	"strings"
)

var (
	ingestPIIEmail = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)
	ingestPIIIP    = regexp.MustCompile(`[0-9A-Za-z_:.]+(?:%[0-9A-Za-z_.-]+)?`)
	ingestPIIIPv4  = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4][0-9]|1?[0-9]?[0-9])\.){3}(?:25[0-5]|2[0-4][0-9]|1?[0-9]?[0-9])\b`)
	ingestPIICard  = regexp.MustCompile(`\b(?:(?:[0-9]{4}[-\s]?){3}[0-9]{4}|3[47][0-9]{2}[-\s]?[0-9]{6}[-\s]?[0-9]{5})\b`)
	ingestPIIPhone = regexp.MustCompile(`(?:\+?1[-.\s])?\(?[0-9]{3}\)?[-.\s][0-9]{3}[-.\s][0-9]{4}`)
	ingestPIISSN   = regexp.MustCompile(`\b[0-9]{3}-[0-9]{2}-[0-9]{4}\b`)
	ingestPIIJWT   = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)
	ingestPIIKey   = regexp.MustCompile(`(?i)\b((?:api[_-]?key|token|bearer|secret|password|auth)[=:\s]+['"]?)[A-Za-z0-9_-]{20,}(['"]?)`)
	ingestPIIUUID  = regexp.MustCompile(`(?i)\b((?:user[_-]?id|session[_-]?id|client[_-]?id|device[_-]?id)[=:\s]+)['"]?[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}['"]?`)
)

func ingestSanitizePII(text string) string {
	text = ingestPIIEmail.ReplaceAllString(text, "[EMAIL_REDACTED]")
	text = ingestPIIIP.ReplaceAllStringFunc(text, ingestRedactIP)
	text = ingestPIICard.ReplaceAllString(text, "[CARD_REDACTED]")
	text = ingestRedactPhones(text)
	text = ingestPIISSN.ReplaceAllString(text, "[SSN_REDACTED]")
	text = ingestPIIJWT.ReplaceAllString(text, "[JWT_REDACTED]")
	text = ingestPIIKey.ReplaceAllString(text, `${1}[KEY_REDACTED]${2}`)
	return ingestPIIUUID.ReplaceAllString(text, `${1}[UUID_REDACTED]`)
}

func ingestRedactIP(token string) string {
	if strings.Count(token, ".") < 3 && strings.Count(token, ":") < 2 {
		return token
	}
	address := strings.TrimRight(token, ".")
	trailing := token[len(address):]
	if ingestIsIPAddress(address) {
		return "[IP_REDACTED]" + trailing
	}
	if prefix, host, ok := strings.Cut(address, ":"); ok {
		if !strings.HasPrefix(address, "::") && ingestIsIPAddress(host) {
			return prefix + ":[IP_REDACTED]" + trailing
		}
	}
	// IPv4 can appear between labels, ports and punctuation. Replace only the address,
	// excluding matches embedded in a longer dotted number (e.g. 1.2.3.4.5).
	var result strings.Builder
	last := 0
	for _, match := range ingestPIIIPv4.FindAllStringIndex(token, -1) {
		start, end := match[0], match[1]
		if start >= 2 && token[start-1] == '.' && token[start-2] >= '0' && token[start-2] <= '9' {
			continue
		}
		if end+1 < len(token) && token[end] == '.' && token[end+1] >= '0' && token[end+1] <= '9' {
			continue
		}
		result.WriteString(token[last:start])
		result.WriteString("[IP_REDACTED]")
		last = end
	}
	if last == 0 {
		return token
	}
	result.WriteString(token[last:])
	return result.String()
}

func ingestIsIPAddress(address string) bool {
	if address == "::" {
		return false
	}
	_, err := netip.ParseAddr(address)
	return err == nil
}

func ingestRedactPhones(text string) string {
	var result strings.Builder
	last := 0
	for _, match := range ingestPIIPhone.FindAllStringIndex(text, -1) {
		start, end := match[0], match[1]
		if start > 0 && (text[start-1] == '-' || ingestIsHex(text[start-1])) || end < len(text) && ingestIsHex(text[end]) {
			continue
		}
		result.WriteString(text[last:start])
		result.WriteString("[PHONE_REDACTED]")
		last = end
	}
	if last == 0 {
		return text
	}
	result.WriteString(text[last:])
	return result.String()
}

func ingestIsHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}
