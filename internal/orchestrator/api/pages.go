package api

import (
	_ "embed"
	"html/template"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed subscribe.html
var subscribePageHTML []byte

//go:embed result.html
var resultPageHTML string

var resultTmpl = template.Must(template.New("result").Parse(resultPageHTML))

// subscribePage serves the public subscribe form. It lives on the orchestrator
// because the orchestrator owns POST /subscribe — the form posts same-origin.
func subscribePage(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", subscribePageHTML)
}

type resultPage struct {
	Icon    string
	Class   string
	Title   string
	Message string
}

func renderResult(c *gin.Context, status int, page resultPage) {
	c.Status(status)
	c.Header("Content-Type", "text/html; charset=utf-8")
	_ = resultTmpl.Execute(c.Writer, page)
}
