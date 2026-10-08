package server

import (
	"net/http"
	"strconv"
)

// Skeleton serves a card in the Foyer widget format
// (https://github.com/audemed44/foyer/blob/main/docs/app-widgets.md).
// Foyer finds it at /api/foyer/widget by itself and calls it with the
// token as a bearer token. Items can also carry an action button, and
// "accepts" offers the app as a destination for files in Foyer's Drop.

type foyerStat struct {
	Label   string `json:"label"`
	Value   string `json:"value"`
	Unit    string `json:"unit,omitempty"`
	Caption string `json:"caption,omitempty"`
	Tone    string `json:"tone,omitempty"` // good, warn, bad or accent
}

type foyerAction struct {
	Label   string `json:"label"`
	URL     string `json:"url"`
	Confirm string `json:"confirm,omitempty"`
}

type foyerItem struct {
	Title    string       `json:"title"`
	Subtitle string       `json:"subtitle,omitempty"`
	Caption  string       `json:"caption,omitempty"`
	URL      string       `json:"url,omitempty"`
	Action   *foyerAction `json:"action,omitempty"`
}

type foyerWidget struct {
	Version     int         `json:"version"`
	Stats       []foyerStat `json:"stats"`
	ItemsTitle  string      `json:"items_title,omitempty"`
	ItemsLayout string      `json:"items_layout,omitempty"` // list or covers
	Items       []foyerItem `json:"items"`
}

func (s *Server) foyerWidget(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.Items(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	done := 0
	for _, it := range items {
		if it.Done {
			done++
		}
	}
	out := foyerWidget{Version: 1, Items: []foyerItem{}, ItemsTitle: "Open", ItemsLayout: "list"}
	out.Stats = append(out.Stats,
		foyerStat{Label: "Open", Value: strconv.Itoa(len(items) - done), Caption: "items", Tone: "accent"},
		foyerStat{Label: "Done", Value: strconv.Itoa(done), Unit: "/" + strconv.Itoa(len(items))},
	)
	for _, it := range items {
		if it.Done {
			continue
		}
		out.Items = append(out.Items, foyerItem{Title: it.Title, Subtitle: it.Note, URL: "/"})
		if len(out.Items) == 12 {
			break
		}
	}
	writeJSON(w, http.StatusOK, out)
}
