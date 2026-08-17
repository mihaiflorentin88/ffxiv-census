package handler

import (
	"encoding/json"
	"net/http"
)

// writeJSON serializes v as the response body with the given status and an
// application/json content type. Encode errors are intentionally ignored:
// the header is already committed, so nothing useful can be written anyway.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes the standard API error envelope {"error": msg}.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
