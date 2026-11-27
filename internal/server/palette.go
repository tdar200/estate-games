package server

import (
	"encoding/json"
	"io/fs"
)

// palettePath is the shared player palette the web apps also read, so the token
// colour a player picks on their phone is the colour the board, plates and
// ownership markers show for them. Format: {"tokens": {"rocket": "#RRGGBB", …}}.
const palettePath = "web/shared/palette.json"

// loadPalette returns the token → colour map: the built-in defaults, overridden by
// the shared palette file when the assets contain a valid one.
func loadPalette(assets fs.FS) map[string]string {
	palette := make(map[string]string, len(tokenColors))
	for kind, color := range tokenColors {
		palette[kind] = color
	}
	if assets == nil {
		return palette
	}
	data, err := fs.ReadFile(assets, palettePath)
	if err != nil {
		return palette
	}
	var file struct {
		Tokens map[string]string `json:"tokens"`
	}
	if json.Unmarshal(data, &file) != nil {
		return palette
	}
	for kind, color := range file.Tokens {
		if validTokenKind(kind) && reColor.MatchString(color) {
			palette[canonicalTokenKind(kind)] = color
		}
	}
	return palette
}

// tokenColor is the display colour for a token kind ("" when unknown).
func (s *Server) tokenColor(kind string) string {
	kind = canonicalTokenKind(kind)
	if s.palette == nil {
		return tokenColors[kind]
	}
	return s.palette[kind]
}

// replaceField sets key to value in a JSON object, keeping key order. The object
// is returned unchanged when it can't be parsed or has no such key.
func replaceField(obj []byte, key string, value json.RawMessage) []byte {
	fields, err := splitObject(obj)
	if err != nil {
		return obj
	}
	for i := range fields {
		if fields[i].key == key {
			fields[i].value = value
			return joinObject(fields)
		}
	}
	return obj
}
