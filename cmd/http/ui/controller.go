package ui

import (
	"html/template"
	"net/http"

	census "github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// UIController handles all web interface views and HTMX partials.
type UIController struct {
	svc *census.Service
	q   contract.Queue
}

// NewUIController constructs a new UI controller.
func NewUIController(svc *census.Service, q contract.Queue) *UIController {
	return &UIController{
		svc: svc,
		q:   q,
	}
}

// PageData represents common data passed to all top-level UI page templates.
type PageData struct {
	Title       string
	ActiveNav   string
	SearchQuery string
	Data        any
}

// render parses layout.html and the target page template, then executes layout.
func (c *UIController) render(w http.ResponseWriter, pageTemplate string, data PageData) {
	tmpl, err := template.New("layout.html").Funcs(templateFuncs).ParseFS(
		content,
		"templates/layout.html",
		pageTemplate,
	)
	if err != nil {
		logging.Error("ui.render", err.Error())
		http.Error(w, "Template rendering error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		logging.Error("ui.render.execute", err.Error())
	}
}

// renderPartial parses a single partial template without layout and executes it.
func (c *UIController) renderPartial(w http.ResponseWriter, partialTemplate string, data any) {
	tmpl, err := template.New("partial").Funcs(templateFuncs).ParseFS(content, partialTemplate)
	if err != nil {
		logging.Error("ui.renderPartial", err.Error())
		http.Error(w, "Partial rendering error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Execute the primary template in the file
	templates := tmpl.Templates()
	targetTmpl := tmpl
	for _, t := range templates {
		if t.Name() != "partial" {
			targetTmpl = t
			break
		}
	}

	if err := targetTmpl.Execute(w, data); err != nil {
		logging.Error("ui.renderPartial.execute", err.Error())
	}
}

// Methodology handles GET /ui/methodology.
func (c *UIController) Methodology(w http.ResponseWriter, r *http.Request) {
	c.render(w, "templates/methodology.html", PageData{
		Title:     "Methodology",
		ActiveNav: "methodology",
	})
}
