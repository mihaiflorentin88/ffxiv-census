package handler

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
	"github.com/mihaiflorentin88/ffxiv-census/port/dto/response"
)

// QueueController exposes work-queue inspection endpoints over HTTP.
type QueueController struct {
	q contract.Queue
}

func NewQueueController(q contract.Queue) *QueueController {
	return &QueueController{q: q}
}

// Depth serves GET /api/v1/queue: provides full overview with status summary and per-event breakdowns.
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

	summary := response.QueueOverviewSummary{
		Pending: depth[contract.QueueJobPending],
		Claimed: depth[contract.QueueJobClaimed],
		Done:    depth[contract.QueueJobDone],
		Failed:  depth[contract.QueueJobFailed],
		ByEvent: make(map[string]response.QueueEventCounts),
	}
	summary.Total = summary.Pending + summary.Claimed + summary.Done + summary.Failed

	sampleLimit := 5
	if raw := r.URL.Query().Get("sample_limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > 50 {
				n = 50
			}
			sampleLimit = n
		}
	}

	details, err := c.q.GetEventDetails(r.Context(), sampleLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	detailsMap := make(map[string]contract.QueueEventDetail, len(details))
	for _, d := range details {
		detailsMap[d.Type] = d
	}

	seen := make(map[string]bool)
	var events []response.QueueEventTypeSummary

	for _, eventType := range canonicalEventTypes {
		seen[eventType] = true
		desc := knownEventDescriptions[eventType]
		d, ok := detailsMap[eventType]
		if !ok {
			d = contract.QueueEventDetail{
				Type:       eventType,
				ActiveJobs: []contract.QueueJob{},
				NextJobs:   []contract.QueueJob{},
				FailedJobs: []contract.QueueJob{},
			}
		}
		events = append(events, response.QueueEventTypeSummary{
			Type:        eventType,
			Description: desc,
			Pending:     d.Pending,
			Claimed:     d.Claimed,
			Done:        d.Done,
			Failed:      d.Failed,
			Total:       d.Total,
			ActiveJobs:  toQueueJobSummaryDTOs(d.ActiveJobs),
			NextJobs:    toQueueJobSummaryDTOs(d.NextJobs),
			FailedJobs:  toQueueJobSummaryDTOs(d.FailedJobs),
		})
	}

	for _, d := range details {
		if !seen[d.Type] {
			desc, ok := knownEventDescriptions[d.Type]
			if !ok {
				desc = "Custom queue event"
			}
			events = append(events, response.QueueEventTypeSummary{
				Type:        d.Type,
				Description: desc,
				Pending:     d.Pending,
				Claimed:     d.Claimed,
				Done:        d.Done,
				Failed:      d.Failed,
				Total:       d.Total,
				ActiveJobs:  toQueueJobSummaryDTOs(d.ActiveJobs),
				NextJobs:    toQueueJobSummaryDTOs(d.NextJobs),
				FailedJobs:  toQueueJobSummaryDTOs(d.FailedJobs),
			})
		}
	}

	for _, e := range events {
		summary.ByEvent[e.Type] = response.QueueEventCounts{
			Pending: e.Pending,
			Claimed: e.Claimed,
			Done:    e.Done,
			Failed:  e.Failed,
			Total:   e.Total,
		}
	}

	writeJSON(w, http.StatusOK, response.QueueOverviewResponse{
		Summary: summary,
		Events:  events,
	})
}

var knownEventDescriptions = map[string]string{
	"id-sweep":           "Probes an ID range on Lodestone for new characters and chains achievement-census",
	"character-census":   "Re-censuses a known character profile and chains achievement-census and fc-census",
	"achievement-census": "Fetches character achievements, updates milestones and latest achievement activity",
	"fc-census":          "Fetches Free Company details and active member counts",
}

var canonicalEventTypes = []string{
	"id-sweep",
	"character-census",
	"achievement-census",
	"fc-census",
}

// Events serves GET /api/v1/queue/events: supported event types, descriptions, and rich job breakdown.
func (c *QueueController) Events(w http.ResponseWriter, r *http.Request) {
	if c.q == nil {
		writeError(w, http.StatusInternalServerError, "queue service unavailable")
		return
	}

	sampleLimit := 5
	if raw := r.URL.Query().Get("sample_limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > 50 {
				n = 50
			}
			sampleLimit = n
		}
	}

	details, err := c.q.GetEventDetails(r.Context(), sampleLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	detailsMap := make(map[string]contract.QueueEventDetail, len(details))
	for _, d := range details {
		detailsMap[d.Type] = d
	}

	seen := make(map[string]bool)
	var result []response.QueueEventTypeSummary

	for _, eventType := range canonicalEventTypes {
		seen[eventType] = true
		desc := knownEventDescriptions[eventType]
		d, ok := detailsMap[eventType]
		if !ok {
			d = contract.QueueEventDetail{
				Type:       eventType,
				ActiveJobs: []contract.QueueJob{},
				NextJobs:   []contract.QueueJob{},
				FailedJobs: []contract.QueueJob{},
			}
		}
		result = append(result, response.QueueEventTypeSummary{
			Type:        eventType,
			Description: desc,
			Pending:     d.Pending,
			Claimed:     d.Claimed,
			Done:        d.Done,
			Failed:      d.Failed,
			Total:       d.Total,
			ActiveJobs:  toQueueJobSummaryDTOs(d.ActiveJobs),
			NextJobs:    toQueueJobSummaryDTOs(d.NextJobs),
			FailedJobs:  toQueueJobSummaryDTOs(d.FailedJobs),
		})
	}

	for _, d := range details {
		if !seen[d.Type] {
			desc, ok := knownEventDescriptions[d.Type]
			if !ok {
				desc = "Custom queue event"
			}
			result = append(result, response.QueueEventTypeSummary{
				Type:        d.Type,
				Description: desc,
				Pending:     d.Pending,
				Claimed:     d.Claimed,
				Done:        d.Done,
				Failed:      d.Failed,
				Total:       d.Total,
				ActiveJobs:  toQueueJobSummaryDTOs(d.ActiveJobs),
				NextJobs:    toQueueJobSummaryDTOs(d.NextJobs),
				FailedJobs:  toQueueJobSummaryDTOs(d.FailedJobs),
			})
		}
	}

	writeJSON(w, http.StatusOK, result)
}

// ListJobs serves GET /api/v1/queue/jobs: paginated jobs filtered by type and/or status.
func (c *QueueController) ListJobs(w http.ResponseWriter, r *http.Request) {
	if c.q == nil {
		writeError(w, http.StatusInternalServerError, "queue service unavailable")
		return
	}
	query := r.URL.Query()

	var filter contract.QueueJobFilter
	filter.Type = query.Get("type")

	if rawStatus := query.Get("status"); rawStatus != "" {
		switch contract.QueueJobStatus(rawStatus) {
		case contract.QueueJobPending, contract.QueueJobClaimed, contract.QueueJobDone, contract.QueueJobFailed:
			filter.Status = contract.QueueJobStatus(rawStatus)
		default:
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
	}

	limit := 50
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if n > 200 {
			n = 200
		}
		limit = n
	}

	offset := 0
	if raw := query.Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid offset")
			return
		}
		offset = n
	}

	jobs, err := c.q.ListJobs(r.Context(), filter, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	total, err := c.q.CountJobs(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	items := make([]response.QueueJobItem, 0, len(jobs))
	for _, j := range jobs {
		items = append(items, toQueueJobItem(j))
	}

	writeJSON(w, http.StatusOK, response.PaginatedQueueJobs{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// GetJob serves GET /api/v1/queue/jobs/{id}: single queue job with payload.
func (c *QueueController) GetJob(w http.ResponseWriter, r *http.Request) {
	if c.q == nil {
		writeError(w, http.StatusInternalServerError, "queue service unavailable")
		return
	}
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}

	job, err := c.q.GetJob(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if job == nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}

	writeJSON(w, http.StatusOK, toQueueJobItem(*job))
}

type retryFailedRequest struct {
	Type  string `json:"type"`
	Limit int    `json:"limit"`
}

// RetryFailed serves POST /api/v1/queue/retry-failed: replays failed jobs back to pending.
func (c *QueueController) RetryFailed(w http.ResponseWriter, r *http.Request) {
	if c.q == nil {
		writeError(w, http.StatusInternalServerError, "queue service unavailable")
		return
	}

	var req retryFailedRequest
	if ct := r.Header.Get("Content-Type"); ct != "" && r.Body != nil {
		if mt, _, err := mime.ParseMediaType(ct); err == nil && mt == "application/json" {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
	}
	if req.Type == "" {
		req.Type = r.URL.Query().Get("type")
	}
	if req.Limit <= 0 {
		if raw := r.URL.Query().Get("limit"); raw != "" {
			req.Limit, _ = strconv.Atoi(raw)
		}
	}
	if req.Limit <= 0 {
		req.Limit = 100
	}

	count, err := c.q.RetryFailed(r.Context(), req.Type, req.Limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, response.QueueRetryFailedResponse{
		Retried: count,
		Message: fmt.Sprintf("successfully replayed %d failed jobs to pending", count),
	})
}

type purgeRequest struct {
	EventType string `json:"event_type"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	OlderThan string `json:"older_than"`
}

// Purge serves POST /api/v1/queue/purge: purges jobs matching event type and status older than a given duration.
func (c *QueueController) Purge(w http.ResponseWriter, r *http.Request) {
	if c.q == nil {
		writeError(w, http.StatusInternalServerError, "queue service unavailable")
		return
	}

	var req purgeRequest
	if ct := r.Header.Get("Content-Type"); ct != "" && r.Body != nil {
		if mt, _, err := mime.ParseMediaType(ct); err == nil && mt == "application/json" {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
	}
	if req.EventType == "" {
		req.EventType = req.Type
	}
	if req.EventType == "" {
		req.EventType = r.URL.Query().Get("event_type")
	}
	if req.EventType == "" {
		req.EventType = r.URL.Query().Get("type")
	}
	if req.Status == "" {
		req.Status = r.URL.Query().Get("status")
	}
	if req.OlderThan == "" {
		req.OlderThan = r.URL.Query().Get("older_than")
	}
	if req.OlderThan == "" || req.OlderThan == "0" {
		req.OlderThan = "0s"
	}
	duration, err := time.ParseDuration(req.OlderThan)
	if err != nil || duration < 0 {
		writeError(w, http.StatusBadRequest, "invalid older_than duration format (e.g. 24h, 30m, 0s)")
		return
	}

	var status contract.QueueJobStatus
	if req.Status != "" && req.Status != "all" {
		status = contract.QueueJobStatus(req.Status)
		if status != contract.QueueJobDone && status != contract.QueueJobFailed && status != contract.QueueJobPending && status != contract.QueueJobClaimed {
			writeError(w, http.StatusBadRequest, "invalid status (allowed: done, failed, pending, claimed, all)")
			return
		}
	}

	purged, err := c.q.PurgeJobs(r.Context(), req.EventType, status, duration)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	displayStatus := req.Status
	if displayStatus == "" {
		displayStatus = "all"
	}
	displayEvent := req.EventType
	if displayEvent == "" {
		displayEvent = "all"
	}

	writeJSON(w, http.StatusOK, response.QueuePurgeResponse{
		Purged:    purged,
		EventType: displayEvent,
		Status:    displayStatus,
		OlderThan: req.OlderThan,
	})
}

func toQueueJobItem(j contract.QueueJob) response.QueueJobItem {
	payload := json.RawMessage(j.Payload)
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	return response.QueueJobItem{
		ID:          j.ID,
		Type:        j.Type,
		Payload:     payload,
		PayloadHash: j.PayloadHash,
		Status:      string(j.Status),
		RunAt:       j.RunAt,
		Attempts:    j.Attempts,
		MaxAttempts: j.MaxAttempts,
		LastError:   j.LastError,
		ClaimedAt:   j.ClaimedAt,
		CreatedAt:   j.CreatedAt,
		FailedAt:    j.FailedAt,
		CompletedAt: j.CompletedAt,
	}
}

func toQueueJobSummaryDTOs(jobs []contract.QueueJob) []response.QueueJobSummaryDTO {
	if len(jobs) == 0 {
		return []response.QueueJobSummaryDTO{}
	}
	out := make([]response.QueueJobSummaryDTO, 0, len(jobs))
	for _, j := range jobs {
		payload := json.RawMessage(j.Payload)
		if len(payload) == 0 {
			payload = json.RawMessage("{}")
		}
		out = append(out, response.QueueJobSummaryDTO{
			ID:          j.ID,
			Type:        j.Type,
			Payload:     payload,
			Status:      string(j.Status),
			Attempts:    j.Attempts,
			MaxAttempts: j.MaxAttempts,
			LastError:   j.LastError,
			ClaimedAt:   j.ClaimedAt,
			RunAt:       j.RunAt,
			CreatedAt:   j.CreatedAt,
			FailedAt:    j.FailedAt,
			CompletedAt: j.CompletedAt,
		})
	}
	return out
}
