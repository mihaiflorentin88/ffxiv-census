package handler

import (
	"encoding/json"
	"net/http"
)

type Controller struct{}

func NewController() Controller {
	return Controller{}
}

// Check exposes a basic liveness probe.
func (c Controller) Check(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
