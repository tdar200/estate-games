package server

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// jsBody is a parsed JSON request body with JavaScript property-access
// semantics: a missing key is undefined, a primitive or array body has no
// properties, and reading a property of a null body throws a TypeError.
type jsBody struct {
	null   bool
	fields map[string]json.RawMessage
}

func parseJSBody(data []byte) (*jsBody, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return nil, errInvalidJSON
	}
	body := &jsBody{fields: map[string]json.RawMessage{}}
	switch trimmed[0] {
	case 'n':
		body.null = true
	case '{':
		if err := json.Unmarshal(trimmed, &body.fields); err != nil {
			return nil, errInvalidJSON
		}
	}
	return body, nil
}

// access mimics reading body.<key> in JS; for a null body it returns the
// TypeError message Node would have reported.
func (b *jsBody) access(key string) error {
	if b.null {
		return &httpError{status: 400, msg: "Cannot read properties of null (reading '" + key + "')"}
	}
	return nil
}

// raw returns the raw JSON of a property, or nil when undefined.
func (b *jsBody) raw(key string) json.RawMessage {
	if b == nil || b.fields == nil {
		return nil
	}
	return b.fields[key]
}

// str is String(body[key] || "").
func (b *jsBody) str(key string) string {
	return jsStringOr(b.raw(key))
}

// exactString returns the property only if it is a JSON string.
func (b *jsBody) exactString(key string) (string, bool) {
	raw := b.raw(key)
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// jsStringOr evaluates String(value || "") for a raw JSON value.
func jsStringOr(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return ""
	}
	if !jsTruthy(value) {
		return ""
	}
	return jsString(value)
}

func jsTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		f, err := v.Float64()
		return err == nil && f != 0 && !math.IsNaN(f)
	default:
		return true
	}
}

// jsString is JavaScript String(value) for decoded JSON values.
func jsString(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		if v {
			return "true"
		}
		return "false"
	case string:
		return v
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return v.String()
		}
		return jsNumberString(f)
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			if item != nil {
				parts[i] = jsString(item)
			}
		}
		return strings.Join(parts, ",")
	case map[string]any:
		return "[object Object]"
	default:
		return ""
	}
}

func jsNumberString(f float64) string {
	if math.IsInf(f, 1) {
		return "Infinity"
	}
	if math.IsInf(f, -1) {
		return "-Infinity"
	}
	if f == 0 {
		return "0"
	}
	abs := math.Abs(f)
	if abs >= 1e21 || abs < 1e-6 {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		// Go: 1e+21, 1.5e-07; JS: 1e+21, 1.5e-7.
		if i := strings.IndexAny(s, "e"); i >= 0 {
			mantissa, exp := s[:i], s[i+1:]
			sign := exp[0]
			digits := strings.TrimLeft(exp[1:], "0")
			if digits == "" {
				digits = "0"
			}
			s = mantissa + "e" + string(sign) + digits
		}
		return s
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// cleanName ports cleanName from src/rooms.js: strip C0 controls and DEL,
// trim, keep at most 12 UTF-16 code units.
func cleanName(name string) (string, error) {
	var b strings.Builder
	for _, r := range name {
		if r <= 0x1f || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	cleaned := jsTrim(b.String())
	cleaned = jsSliceUTF16(cleaned, 12)
	if cleaned == "" {
		return "", badRequest("Enter a player name")
	}
	return cleaned, nil
}

// jsTrim trims the characters String.prototype.trim removes.
func jsTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
			return true
		}
		return r >= 0x2000 && r <= 0x200a
	})
}

// jsSliceUTF16 is s.slice(0, n) measured in UTF-16 code units. A surrogate
// pair cut in half is dropped (JS would keep a lone surrogate, which is not
// representable in Go strings).
func jsSliceUTF16(s string, n int) string {
	units := 0
	for i, r := range s {
		width := 1
		if r >= 0x10000 {
			width = 2
		}
		if units+width > n {
			return s[:i]
		}
		units += width
	}
	return s
}

// utf16Len counts UTF-16 code units, like JS string length.
func utf16Len(data []byte) int {
	units := 0
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		data = data[size:]
		if r >= 0x10000 {
			units += 2
		} else {
			units++
		}
	}
	return units
}

// unescapeHTMLInJSON reverses encoding/json's HTML escaping (<, >,
// &) so responses match JSON.stringify byte for byte. Backslashes only
// occur inside JSON strings, so a simple escape-aware scan is exact.
func unescapeHTMLInJSON(data []byte) []byte {
	if !bytes.Contains(data, []byte(`\u00`)) {
		return data
	}
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		c := data[i]
		if c != '\\' || i+1 >= len(data) {
			out = append(out, c)
			continue
		}
		if data[i+1] == 'u' && i+5 < len(data) {
			switch string(data[i+2 : i+6]) {
			case "003c":
				out = append(out, '<')
				i += 5
				continue
			case "003e":
				out = append(out, '>')
				i += 5
				continue
			case "0026":
				out = append(out, '&')
				i += 5
				continue
			}
		}
		out = append(out, c, data[i+1])
		i++
	}
	return out
}

// marshalJS encodes like JSON.stringify (no HTML escaping, no newline).
func marshalJS(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return unescapeHTMLInJSON(bytes.TrimRight(buf.Bytes(), "\n")), nil
}
