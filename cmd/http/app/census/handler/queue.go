package handler

import (
	"net/http"
	"sort"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
	"github.com/mihaiflorentin88/ffxiv-census/port/dto/response"
)

// QueueController exposes the work-queue depth over HTTP.
type QueueController struct {
	q contract.Queue
}

func NewQueueController(q contract.Queue) *QueueController {
	return &QueueController{q: q}
}

// Depth serves GET /api/v1/queue: one entry per job status, sorted by status.
func (c *QueueController) Depth(w http.ResponseWriter, r *http.Request) {
	if c.q == nil {
		writeError(w, http.StatusInternalServerError, "queue service unavailable")
		return
	}
	depth, err := c.q.Depth(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]response.QueueDepthItem, 0, len(depth))
	for status, count := range depth {
		items = append(items, response.QueueDepthItem{Status: string(status), Count: count})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Status < items[j].Status })
	writeJSON(w, http.StatusOK, items)
}
