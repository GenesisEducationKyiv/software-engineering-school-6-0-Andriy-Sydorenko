// Package templates embeds the HTML used for emails and public pages.
package templates

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
)

//go:embed emails/*.html
var fs embed.FS

var emailTmpl = template.Must(template.ParseFS(fs, "emails/*.html"))

func RenderEmail(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := emailTmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("render email %s: %w", name, err)
	}
	return buf.String(), nil
}
