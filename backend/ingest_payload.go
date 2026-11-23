package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

type ingestTimestamp struct{ time.Time }

func (t *ingestTimestamp) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	for _, format := range []string{time.RFC3339Nano, "2006-01-02T15:04Z07:00"} {
		if parsed, err := time.Parse(format, value); err == nil {
			t.Time = parsed.UTC()
			return nil
		}
	}
	for _, format := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", time.DateOnly} {
		if parsed, err := time.ParseInLocation(format, value, time.Local); err == nil {
			t.Time = parsed.UTC()
			return nil
		}
	}
	return errors.New("invalid timestamp")
}

type ingestSystemProps struct {
	IsDebug        bool   `json:"isDebug"`
	OSName         string `json:"osName"`
	OSVersion      string `json:"osVersion"`
	DeviceModel    string `json:"deviceModel"`
	Locale         string `json:"locale"`
	AppVersion     string `json:"appVersion"`
	AppBuildNumber string `json:"appBuildNumber"`
	EngineName     string `json:"engineName"`
	EngineVersion  string `json:"engineVersion"`
	SDKVersion     string `json:"sdkVersion"`
}

type ingestEventBody struct {
	EventName   string             `json:"eventName"`
	Timestamp   *ingestTimestamp   `json:"timestamp"`
	SessionID   string             `json:"sessionId"`
	SystemProps *ingestSystemProps `json:"systemProps"`
	Props       json.RawMessage    `json:"props"`
	stringProps string
	numberProps string
}

type ingestLength struct {
	value string
	max   int
}

func ingestTextLen(value string) int {
	length := 0
	for _, r := range value {
		length += utf16.RuneLen(r)
	}
	return length
}

func ingestFits(fields ...ingestLength) bool {
	for _, field := range fields {
		if ingestTextLen(field.value) > field.max {
			return false
		}
	}
	return true
}

func (e *ingestEventBody) validate(now time.Time) string {
	p := e.SystemProps
	if p == nil || strings.TrimSpace(e.EventName) == "" || strings.TrimSpace(e.SessionID) == "" || strings.TrimSpace(p.SDKVersion) == "" {
		return "Invalid event payload"
	}
	if !ingestFits(ingestLength{e.EventName, 60}, ingestLength{p.OSName, 30}, ingestLength{p.OSVersion, 100},
		ingestLength{p.DeviceModel, 100}, ingestLength{p.Locale, 10}, ingestLength{p.AppVersion, 50},
		ingestLength{p.AppBuildNumber, 20}, ingestLength{p.EngineName, 30}, ingestLength{p.EngineVersion, 30},
		ingestLength{p.SDKVersion, 40}) {
		return "Invalid event payload"
	}
	if numericID, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(e.SessionID), "+"), 10, 64); err == nil {
		started := time.Unix(int64(numericID/100_000_000), 0)
		if started.After(now.Add(10 * time.Minute)) {
			return "Future sessions are not allowed."
		}
		if started.Before(now.Add(-7 * 24 * time.Hour)) {
			return "Session is too old."
		}
	} else if ingestTextLen(e.SessionID) > 36 {
		return "SessionId must be less than or equal to 36 characters."
	}
	if e.Timestamp == nil || e.Timestamp.Before(now.Add(-24*time.Hour)) {
		return "Event is too old."
	}
	if e.Timestamp.After(now) {
		e.Timestamp.Time = now
	}
	p.Locale = ingestLocale(p.Locale)
	return e.splitProps()
}

func (e *ingestEventBody) splitProps() string {
	props := bytes.TrimSpace(e.Props)
	stringProps := make(map[string]string)
	numberProps := make(map[string]json.Number)
	if len(props) > 0 && !bytes.Equal(props, []byte("null")) {
		if props[0] == '"' {
			var value string
			if err := json.Unmarshal(props, &value); err != nil {
				return "Props must be an object or a valid stringified JSON."
			}
			props = bytes.TrimSpace([]byte(value))
		}
		var values map[string]json.RawMessage
		if len(props) == 0 || props[0] != '{' || json.Unmarshal(props, &values) != nil {
			return "Props must be an object or a valid stringified JSON."
		}
		for key, value := range values {
			if strings.TrimSpace(key) == "" {
				return "Property key must not be empty."
			}
			if ingestTextLen(key) > 40 {
				return "Property key must be less than or equal to 40 characters."
			}
			switch value[0] {
			case '"':
				var text string
				if err := json.Unmarshal(value, &text); err != nil {
					return "Invalid string property."
				}
				stringProps[key] = ingestTruncate(text, 180)
			case 't', 'f':
				stringProps[key] = string(value)
			case '[':
				stringProps[key] = "[Array]"
			case '{':
				stringProps[key] = "{Object}"
			case 'n':
				stringProps[key] = ""
			default:
				number := json.Number(value)
				if _, err := number.Float64(); err != nil {
					return "Numeric property is out of range."
				}
				numberProps[key] = number
			}
		}
	}
	stringsJSON, _ := json.Marshal(stringProps)
	numbersJSON, _ := json.Marshal(numberProps)
	e.stringProps, e.numberProps = string(stringsJSON), string(numbersJSON)
	return ""
}

func ingestTruncate(value string, maxLength int) string {
	if ingestTextLen(value) <= maxLength {
		return value
	}
	count := 0
	for idx, r := range value {
		count += utf16.RuneLen(r)
		if count > maxLength-3 {
			return value[:idx] + "..."
		}
	}
	return value
}

func ingestLocale(locale string) string {
	if locale == "C" {
		return ""
	}
	locale = strings.ToLower(strings.ReplaceAll(locale, "_", "-"))
	switch locale {
	case "en-001", "en-150", "en-us-posix":
		return "en"
	}
	chars := utf16.Encode([]rune(locale))
	if n := len(chars); n == 2 || ((n >= 5 && n <= 7) || (n == 10 && chars[7] == '-')) && chars[2] == '-' {
		return locale
	}
	return ""
}

var ingestUserAgentParts = regexp.MustCompile(`\((.*?)\)(\s|$)|([^\s/]+)/(\S+)(\s|$)`)

func (e *ingestEventBody) enrichAgent(agent string) string {
	p := e.SystemProps
	if p.OSName != "" {
		return p.OSName + "/" + p.OSVersion + " " + p.EngineName + "/" + p.EngineVersion + " " + p.Locale
	}
	if agent == "" {
		return ""
	}
	p.OSName, p.OSVersion = "", ""
	lower := strings.ToLower(agent)
	for _, os := range [...]struct{ token, name string }{
		{"ipad", "iPadOS"}, {"iphone", "iOS"}, {"ios", "iOS"}, {"windows nt", "Windows"},
		{"mac os x", "macOS"}, {"macos", "macOS"}, {"macintosh", "macOS"}, {"android", "Android"},
		{"cros", "Chrome OS"}, {"ubuntu", "Ubuntu"}, {"fedora", "Fedora"}, {"arch linux", "Arch Linux"},
		{"archlinux", "Arch Linux"}, {"linux", "Linux"},
	} {
		if strings.Contains(lower, os.token) {
			p.OSName = os.name
			break
		}
	}
	p.EngineName, p.EngineVersion = ingestBrowser(agent)
	return agent
}

func ingestBrowser(agent string) (string, string) {
	parts := ingestUserAgentParts.FindAllStringSubmatch(agent, -1)
	var chrome string
	for _, part := range parts {
		name, version := part[3], part[4]
		switch name {
		case "Edg":
			return "Edge", version
		case "Firefox":
			return "Firefox", version
		case "OPiOS", "OPR":
			return "Opera", version
		case "YaBrowser":
			return "Yandex Browser", version
		case "Chrome":
			chrome = version
		case "Safari":
			for _, candidate := range parts {
				if candidate[3] == "Version" {
					return "Safari", candidate[4]
				}
			}
		}
	}
	if chrome != "" {
		return "Chrome", chrome
	}
	return "", ""
}

func (e *ingestEventBody) row(appID, country, region string) map[string]any {
	p := e.SystemProps
	ttlDays := 5 * 365
	if p.IsDebug {
		appID += "_DEBUG"
		ttlDays = 182
	}
	return map[string]any{
		"app_id": appID, "timestamp": e.Timestamp.Time, "event_name": e.EventName,
		"user_id": "", "session_id": e.SessionID, "os_name": p.OSName, "os_version": p.OSVersion,
		"device_model": p.DeviceModel, "locale": p.Locale, "app_version": p.AppVersion,
		"app_build_number": p.AppBuildNumber, "engine_name": p.EngineName, "engine_version": p.EngineVersion,
		"sdk_version": p.SDKVersion, "country_code": country, "region_name": region, "city": "",
		"string_props": e.stringProps, "numeric_props": e.numberProps, "ttl": e.Timestamp.AddDate(0, 0, ttlDays),
	}
}

type ingestErrorBody struct {
	ErrorMessage string           `json:"errorMessage"`
	ErrorType    string           `json:"errorType"`
	StackTrace   string           `json:"stackTrace"`
	Timestamp    *ingestTimestamp `json:"timestamp"`
	Platform     string           `json:"platform"`
	OSName       string           `json:"osName"`
	OSVersion    string           `json:"osVersion"`
	AppVersion   string           `json:"appVersion"`
	SDKVersion   string           `json:"sdkVersion"`
	SessionID    string           `json:"sessionId"`
	Severity     string           `json:"severity"`
	Kind         string           `json:"kind"`
	IsDebug      bool             `json:"isDebug"`
}

func (e ingestErrorBody) valid() bool {
	if strings.TrimSpace(e.ErrorMessage) == "" || strings.TrimSpace(e.ErrorType) == "" {
		return false
	}
	return (e.Timestamp == nil || !e.Timestamp.Before(time.Unix(0, 0))) &&
		ingestFits(ingestLength{e.ErrorMessage, 5000}, ingestLength{e.ErrorType, 100}, ingestLength{e.StackTrace, 10000},
			ingestLength{e.Platform, 30}, ingestLength{e.OSName, 30}, ingestLength{e.OSVersion, 100},
			ingestLength{e.AppVersion, 50}, ingestLength{e.SDKVersion, 40}, ingestLength{e.SessionID, 100},
			ingestLength{e.Severity, 20}, ingestLength{e.Kind, 20})
}

func (e ingestErrorBody) row(appID string, now time.Time) (map[string]any, error) {
	id, err := ingestErrorID()
	if err != nil {
		return nil, err
	}
	timestamp := now
	if e.Timestamp != nil && e.Timestamp.Before(now) {
		timestamp = e.Timestamp.Time
	}
	if e.IsDebug {
		appID += "_DEBUG"
	}
	return map[string]any{
		"error_id": id, "app_id": appID, "timestamp": timestamp,
		"error_message": ingestSanitizePII(e.ErrorMessage), "error_type": e.ErrorType,
		"stack_trace": ingestSanitizePII(e.StackTrace), "platform": e.Platform, "os_name": e.OSName,
		"os_version": e.OSVersion, "app_version": e.AppVersion, "sdk_version": e.SDKVersion, "session_id": e.SessionID,
		"severity": ingestCanonical(e.Severity, "fatal", "error"), "kind": ingestCanonical(e.Kind, "crash", "unhandled", "taskException", "handled"),
		"ttl": timestamp.AddDate(0, 0, 180),
	}, nil
}

func ingestCanonical(value string, options ...string) string {
	for _, option := range options {
		if strings.EqualFold(option, value) {
			return option
		}
	}
	return ""
}

func ingestErrorID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return hex.EncodeToString(id[:4]) + "-" + hex.EncodeToString(id[4:6]) + "-" + hex.EncodeToString(id[6:8]) + "-" + hex.EncodeToString(id[8:10]) + "-" + hex.EncodeToString(id[10:]), nil
}
