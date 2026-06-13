// Package templates embeds the HTML used for public pages.
package templates

import (
	"embed"
	"fmt"
)

//go:embed pages/*.html
var fs embed.FS

func Page(name string) ([]byte, error) {
	b, err := fs.ReadFile("pages/" + name)
	if err != nil {
		return nil, fmt.Errorf("read page %s: %w", name, err)
	}
	return b, nil
}
