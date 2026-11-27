package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// QuoteRequest is the trade-quote request body
// ({"targetId":…, "offerProperties":[…], "requestProperties":[…]}; other keys
// such as "token" are ignored).
//
// The fields keep the raw JSON so QuoteTrade can apply the same coercions as
// quoteAiTrade: targetId must be a JSON string to match a player (JS ===),
// and property lists go through Array.isArray and Number(x) (so "5", [5] and
// 5.0 all mean 5, a non-array means "no properties", and anything that is not
// an integer board index of an ownable square is rejected). A missing key and
// JSON null both behave like JS undefined.
type QuoteRequest struct {
	TargetID          json.RawMessage `json:"targetId,omitempty"`
	OfferProperties   json.RawMessage `json:"offerProperties,omitempty"`
	RequestProperties json.RawMessage `json:"requestProperties,omitempty"`
}

// NewQuoteRequest builds a QuoteRequest from Go values.
func NewQuoteRequest(targetID string, offerProperties, requestProperties []int) QuoteRequest {
	target, _ := json.Marshal(targetID)
	offer, _ := json.Marshal(nonNil(offerProperties))
	request, _ := json.Marshal(nonNil(requestProperties))
	return QuoteRequest{TargetID: target, OfferProperties: offer, RequestProperties: request}
}

func nonNil(list []int) []int {
	if list == nil {
		return []int{}
	}
	return list
}

// targetID returns the target id when the raw value is a JSON string.
func (q QuoteRequest) targetID() (string, bool) {
	raw := bytes.TrimSpace(q.TargetID)
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var id string
	if err := json.Unmarshal(raw, &id); err != nil {
		return "", false
	}
	return id, true
}

var errInvalidQuoteProperties = errors.New("The selected quote properties are invalid")

// normalizeQuoteProperties mirrors normalizeQuoteProperties(properties):
// [...new Set((Array.isArray(p) ? p : []).map(Number))], then every entry
// must be an integer index of an ownable square.
func normalizeQuoteProperties(raw json.RawMessage) ([]int, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return []int{}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return []int{}, nil
	}
	var values []float64
	for _, item := range items {
		v := jsonToNumber(item)
		duplicate := false
		for _, seen := range values {
			// Set membership is SameValueZero: NaN equals NaN, +0 equals -0.
			if seen == v || (math.IsNaN(seen) && math.IsNaN(v)) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			values = append(values, v)
		}
	}
	indexes := make([]int, 0, len(values))
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) || v < 0 || v >= boardSize {
			return nil, errInvalidQuoteProperties
		}
		index := int(v)
		if !board[index].ownable() {
			return nil, errInvalidQuoteProperties
		}
		indexes = append(indexes, index)
	}
	return indexes, nil
}

// jsonToNumber is JS Number(value) for a decoded JSON value.
func jsonToNumber(raw json.RawMessage) float64 {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return math.NaN() // undefined
	}
	switch raw[0] {
	case 'n':
		return 0
	case 't':
		return 1
	case 'f':
		return 0
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return math.NaN()
		}
		return stringToNumber(s)
	case '{':
		return math.NaN() // "[object Object]"
	case '[':
		// Number(array) is Number(array.join(",")): [] -> "", [x] -> String(x),
		// two or more elements always contain a comma and are NaN.
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return math.NaN()
		}
		switch len(items) {
		case 0:
			return 0
		case 1:
			return arrayElementToNumber(items[0])
		}
		return math.NaN()
	}
	v, _ := strconv.ParseFloat(string(raw), 64) // ±Inf on overflow, like JS
	return v
}

// arrayElementToNumber is Number(String(x)) for x inside an array, where join
// renders null as "".
func arrayElementToNumber(raw json.RawMessage) float64 {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return math.NaN()
	}
	switch raw[0] {
	case 'n':
		return 0 // ""
	case 't', 'f':
		return math.NaN() // "true" / "false"
	}
	return jsonToNumber(raw)
}

var (
	jsDecimal = regexp.MustCompile(`^[+-]?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
	jsHex     = regexp.MustCompile(`^0[xX][0-9a-fA-F]+$`)
	jsOctal   = regexp.MustCompile(`^0[oO][0-7]+$`)
	jsBinary  = regexp.MustCompile(`^0[bB][01]+$`)
)

// jsWhitespace is what StringToNumber trims: WhiteSpace and LineTerminator.
var jsWhitespace = string([]rune{
	0x0009, 0x000a, 0x000b, 0x000c, 0x000d, 0x0020, 0x00a0, 0x1680,
	0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200a,
	0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff,
})

// stringToNumber is JS Number(string).
func stringToNumber(s string) float64 {
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
		v, _ := strconv.ParseFloat(s, 64)
		return v
	}
	return math.NaN()
}

// parseRadix converts digits in base 2, 8 or 16. Precision beyond 2^53 does
// not matter here: such values are never board indexes.
func parseRadix(digits string, base int) float64 {
	v := 0.0
	for _, r := range digits {
		d, _ := strconv.ParseInt(string(r), base, 64)
		v = mul(v, float64(base)) + float64(d)
	}
	return v
}
