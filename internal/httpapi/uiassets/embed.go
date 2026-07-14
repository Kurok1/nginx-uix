/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package uiassets

import (
	"embed"
	"io/fs"
)

// embedded contains the production Vite output and a local empty anchor.
//
//go:embed all:dist
var embedded embed.FS

// FS returns the embedded distribution root.
func FS() fs.FS {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic("embedded UI distribution is missing")
	}
	return dist
}
