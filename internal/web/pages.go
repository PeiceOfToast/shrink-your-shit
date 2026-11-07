// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"net/http"
	"slices"
)

//go:embed templates
var templates embed.FS

// navItem is one menu entry. static/nav.json is the single list of them; the
// browser reads the same file to add the entries that depend on settings.
type navItem struct {
	ID                       string   `json:"id"`
	Label                    string   `json:"label"`
	Href                     string   `json:"href"`
	ShowAt                   []string `json:"showAt"`                   // control levels that show it; empty = always
	ShowWithPerLibraryChoice bool     `json:"showWithPerLibraryChoice"` // also shown when "Let me choose per library" is on
}

func (n navItem) visible(level string, perLibrary bool) bool {
	if len(n.ShowAt) == 0 {
		return true
	}
	return slices.Contains(n.ShowAt, level) || (n.ShowWithPerLibraryChoice && perLibrary)
}

// navFor lists the menu items shown at a control level.
func navFor(items []navItem, level string, perLibrary bool) []navItem {
	var out []navItem
	for _, n := range items {
		if n.visible(level, perLibrary) {
			out = append(out, n)
		}
	}
	return out
}

func loadNav() []navItem {
	b, err := fs.ReadFile(static, "static/nav.json")
	if err != nil {
		panic(err)
	}
	var items []navItem
	if err := json.Unmarshal(b, &items); err != nil {
		panic(err)
	}
	return items
}

type tab struct{ Label, Href string }

// page is one screen. Section is a nav id; Tab names the page within a section's tab row.
type page struct {
	Path, Section, Tab, Kind, Empty string
}

var activityTabs = []tab{
	{"Queue", "/activity/"}, {"Skipped", "/activity/skipped/"}, {"Failed", "/activity/failed/"},
	{"Done", "/activity/done/"}, {"Kept originals", "/activity/kept/"},
}
var settingsTabs = []tab{
	{"General", "/settings/"}, {"Safety", "/settings/safety/"}, {"Performance", "/settings/performance/"},
	{"Integrations", "/settings/integrations/"}, {"System", "/settings/system/"},
}

var pages = []page{
	{Path: "/welcome/", Kind: "wizard", Tab: "Welcome"},
	{Path: "/libraries/", Section: "libraries", Kind: "libraries", Empty: "Your libraries will show up here. A library is a folder of videos you want to make smaller."},
	{Path: "/library/", Section: "libraries", Kind: "library", Tab: "Library"},
	{Path: "/activity/", Section: "activity", Kind: "activity", Tab: "Queue", Empty: "Nothing is being converted right now. Files waiting for their turn will be listed here."},
	{Path: "/activity/skipped/", Section: "activity", Kind: "activity", Tab: "Skipped", Empty: "Files that were left alone, and why, will be listed here."},
	{Path: "/activity/failed/", Section: "activity", Kind: "activity", Tab: "Failed", Empty: "Files that could not be converted will be listed here. Your originals are never touched when something fails."},
	{Path: "/activity/done/", Section: "activity", Kind: "activity", Tab: "Done", Empty: "Converted files will be listed here."},
	{Path: "/activity/kept/", Section: "activity", Kind: "activity", Tab: "Kept originals", Empty: "Originals we are keeping for you will be listed here, with the date they will be deleted."},
	{Path: "/flow/", Section: "flows", Kind: "flow", Tab: "Flow"},
	{Path: "/flows/", Section: "flows", Kind: "flows", Empty: "Your flows will be listed here."},
	{Path: "/workers/", Section: "workers", Kind: "workers"},
	{Path: "/settings/", Section: "settings", Tab: "General", Kind: "settings"},
	{Path: "/settings/safety/", Section: "settings", Tab: "Safety", Kind: "settings"},
	{Path: "/settings/performance/", Section: "settings", Tab: "Performance", Kind: "settings"},
	{Path: "/settings/integrations/", Section: "settings", Tab: "Integrations", Kind: "settings"},
	{Path: "/settings/system/", Section: "settings", Tab: "System", Kind: "settings"},
}

type tabView struct {
	Label, Href string
	Current     bool
}

// scripts lists what a page loads: the shared core, then only what that screen needs. mock.js is the
// stand-in server and goes away when the real endpoints land.
func scripts(kind string) []string {
	core := []string{"api.js", "mock.js", "app.js"}
	switch kind {
	case "wizard":
		return append(core, "ui.js", "folder.js", "editors.js", "firstrun.js")
	case "libraries":
		return append(core, "ui.js", "badge.js", "folder.js", "libraries.js")
	case "library":
		return append(core, "ui.js", "badge.js", "editors.js", "library.js")
	case "flows":
		return append(core, "ui.js", "flows.js")
	case "flow":
		return append(core, "ui.js", "editors.js", "flow.js")
	case "activity":
		return append(core, "ui.js", "activity.js")
	case "settings":
		return append(core, "ui.js", "settings.js")
	case "workers":
		return append(core, "ui.js", "workers.js")
	}
	return core
}

// lazyScripts are loaded by a page's own script, only when a setting calls for them.
func lazyScripts(kind string) []string {
	switch kind {
	case "library":
		return []string{"flowpick.js"} // the Flow choice, only with "Let me choose per library"
	}
	return nil
}

type pageView struct {
	Scripts                              []string
	Tab                                  string
	Title, Heading, Section, Kind, Empty string
	Nav                                  []navItem
	Tabs                                 []tabView
}

// render builds one page as the server sends it: the menu at the default
// control level (Basic); the browser adds what the current settings allow.
func render(items []navItem, p page) ([]byte, error) {
	var heading string
	for _, n := range items {
		if n.ID == p.Section {
			heading = n.Label
		}
	}
	v := pageView{Title: heading, Heading: heading, Section: p.Section, Kind: p.Kind, Empty: p.Empty, Scripts: scripts(p.Kind), Tab: p.Tab}
	if p.Kind == "wizard" { // the top bar has no menu during first-run
		v.Title, v.Heading = "Set up", "Set up"
	} else {
		v.Nav = navFor(items, "basic", false)
	}
	if p.Tab != "" && p.Kind != "wizard" && p.Kind != "library" && p.Kind != "flow" {
		v.Title = p.Tab + " - " + heading
		list := activityTabs
		if p.Section == "settings" {
			list = settingsTabs
		}
		for _, t := range list {
			v.Tabs = append(v.Tabs, tabView{t.Label, t.Href, t.Label == p.Tab})
		}
	}
	var buf bytes.Buffer
	err := pageTmpl.Execute(&buf, v)
	return buf.Bytes(), err
}

var pageTmpl = template.Must(template.ParseFS(templates, "templates/*.html"))

func pageHandler(items []navItem, p page) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := render(items, p)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	}
}
