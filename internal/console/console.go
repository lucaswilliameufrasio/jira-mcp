// Package console renders the read-only operational console.
package console

import (
	_ "embed"
	"html/template"
	"io"
	"time"

	"jira-mcp/internal/mcp"
)

//go:embed console.html
var markup string

//go:embed console.css
var stylesheet string

var pageTemplate = template.Must(template.New("console").Parse(markup))

type Event struct {
	Tool     string
	Time     string
	Duration string
	Success  bool
}

type Page struct {
	Section     string
	Title       string
	Account     string
	Site        string
	CSRF        string
	Local       bool
	GeneratedAt string
	Unavailable bool
	Count       int
	Failures    int
	Average     string
	Events      []Event
	Style       template.CSS
}

// NewPage summarizes only the supplied bounded sample, not the entire history.
func NewPage(section, account, site, csrf string, events []mcp.ToolEvent, unavailable bool, now time.Time) Page {
	page := Page{Section: section, Account: account, Site: site, CSRF: csrf, Unavailable: unavailable, GeneratedAt: now.UTC().Format("02/01/2006 15:04:05 UTC"), Style: template.CSS(stylesheet)}
	switch section {
	case "monitoring":
		page.Title = "Monitoramento"
	case "audit":
		page.Title = "Auditoria"
	default:
		page.Section = "overview"
		page.Title = "Visão geral"
	}
	var total time.Duration
	for _, event := range events {
		page.Count++
		if !event.Success {
			page.Failures++
		}
		total += event.Duration
		page.Events = append(page.Events, Event{Tool: event.Tool, Time: event.StartedAt.UTC().Format("02/01/2006 15:04:05 UTC"), Duration: event.Duration.Round(time.Millisecond).String(), Success: event.Success})
	}
	if page.Count > 0 {
		page.Average = (total / time.Duration(page.Count)).Round(time.Millisecond).String()
	}
	return page
}

func Render(w io.Writer, page Page) error {
	return pageTemplate.Execute(w, page)
}
