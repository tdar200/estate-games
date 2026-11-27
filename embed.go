// Package estate embeds the static front-ends served by the estate binary:
// web/ (PC table at web/table, phone at web/phone, shared modules, demo
// fixtures) and public/ (the classic client, served under /classic/).
package estate

import "embed"

// Assets holds web/ and public/ at its root, e.g. "web/table/index.html".
//
//go:embed web public
var Assets embed.FS
