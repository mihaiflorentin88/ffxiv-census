package ui

import (
	"net/http"
)

// Favicon serves the embedded site icon at the conventional favicon path so
// browsers and search results can display it.
func (c *UIController) Favicon(w http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(w, r, content, "assets/favicon.png")
}
