package engine

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// This file reproduces the JS coercions game.js applies to client input
// (Number(x), Number.isInteger, BOARD[index]) for values decoded from JSON.

var (
	jsDecimal = regexp.MustCompile(`^[+-]?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
	jsHex     = regexp.MustCompile(`^0[xX][0-9a-fA-F]+$`)
	jsOctal   = regexp.MustCompile(`^0[oO][0-7]+$`)
	jsBinary  = regexp.MustCompile(`^0[bB][01]+$`)
)

// jsWhitespace is the set of characters String.prototype.trim removes
// (ASCII whitespace, NBSP, Unicode spaces, line separators and the BOM).
var jsWhitespace = string([]rune{
	0x0009, 0x000a, 0x000b, 0x000c, 0x000d, 0x0020,
	0x00a0, 0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007,
	0x2008, 0x2009, 0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff,
})

// jsStringToNumber implements Number(string).
func jsStringToNumber(s string) float64 {
	s = strings.Trim(s, jsWhitespace)
	switch {
	case s == "":
		return 0
	case s == "Infinity" || s == "+Infinity":
		return math.Inf(1)
	case s == "-Infinity":
		return math.Inf(-1)
	case jsHex.MatchString(s):
		return parseRadix(s[2:], 16)
	case jsOctal.MatchString(s):
		return parseRadix(s[2:], 8)
	case jsBinary.MatchString(s):
		return parseRadix(s[2:], 2)
	case jsDecimal.MatchString(s):
		value, err := strconv.ParseFloat(s, 64)
		if err != nil {
			// Overflow yields ±Inf like JS; anything else is not a number.
			if numErr, ok := err.(*strconv.NumError); ok && numErr.Err == strconv.ErrRange {
				return value
			}
			return math.NaN()
		}
		return value
	}
	return math.NaN()
}

func parseRadix(digits string, base float64) float64 {
	value := 0.0
	for _, digit := range digits {
		d, _ := strconv.ParseInt(string(digit), int(base), 64)
		value = value*base + float64(d)
	}
	return value
}

// jsNumber implements Number(value) for values produced by encoding/json
// (with UseNumber) or set directly from Go.
func jsNumber(value any) float64 {
	switch v := value.(type) {
	case nil:
		return 0
	case bool:
		if v {
			return 1
		}
		return 0
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		return jsStringToNumber(string(v))
	case string:
		return jsStringToNumber(v)
	case []any:
		// Number(array) === Number(array.join(",")).
		switch len(v) {
		case 0:
			return 0
		case 1:
			return jsArrayElementNumber(v[0])
		default:
			return math.NaN() // the joined string contains a comma
		}
	}
	return math.NaN() // objects: "[object Object]"
}

// jsArrayElementNumber is Number(String(element)) for the single element of
// an array (null and undefined join as the empty string).
func jsArrayElementNumber(value any) float64 {
	switch v := value.(type) {
	case nil:
		return 0
	case bool:
		return math.NaN() // "true" / "false"
	case []any:
		return jsNumber(v)
	case map[string]any:
		return math.NaN()
	}
	return jsNumber(value)
}

// jsTruthy reports JS truthiness for JSON-decoded values.
func jsTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		f := jsStringToNumber(string(v))
		return f != 0 && !math.IsNaN(f)
	case float64:
		return v != 0 && !math.IsNaN(v)
	case int:
		return v != 0
	}
	return true
}

// isInteger implements Number.isInteger.
func isInteger(value float64) bool {
	return !math.IsInf(value, 0) && !math.IsNaN(value) && math.Trunc(value) == value
}

// boardIndex converts a coerced number to a board index the way BOARD[n]
// does: only integers 0..39 (including -0) address a space.
func boardIndex(value float64) (int, bool) {
	if !isInteger(value) || value < 0 || value >= BoardSize {
		return -1, false
	}
	return int(value), true
}
