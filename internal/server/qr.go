package server

import (
	"fmt"
	"strings"

	"rsc.io/qr"
)

const qrQuietZone = 4

// qrSVG renders text as a QR code SVG: one module per user unit, a 4-module
// quiet zone, black modules drawn as a single path on a white background.
func qrSVG(text string) ([]byte, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, err
	}
	size := code.Size + 2*qrQuietZone
	var path strings.Builder
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if !code.Black(x, y) {
				continue
			}
			// Merge horizontal runs into one rectangle.
			run := 1
			for x+run < code.Size && code.Black(x+run, y) {
				run++
			}
			fmt.Fprintf(&path, "M%d %dh%dv1h-%dz", x+qrQuietZone, y+qrQuietZone, run, run)
			x += run - 1
		}
	}
	var svg strings.Builder
	fmt.Fprintf(&svg, `<?xml version="1.0" encoding="UTF-8"?>`+"\n")
	fmt.Fprintf(&svg, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" shape-rendering="crispEdges" role="img" aria-label="QR code">`, size, size, size*8, size*8)
	fmt.Fprintf(&svg, `<title>%s</title>`, xmlEscape(text))
	fmt.Fprintf(&svg, `<rect width="%d" height="%d" fill="#ffffff"/>`, size, size)
	fmt.Fprintf(&svg, `<path fill="#000000" d="%s"/>`, path.String())
	svg.WriteString("</svg>\n")
	return []byte(svg.String()), nil
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}
