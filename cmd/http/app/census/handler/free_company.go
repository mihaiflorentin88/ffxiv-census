package handler

import (
	"net/http"
	"strconv"

	census "github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
	"github.com/mihaiflorentin88/ffxiv-census/port/dto/response"
)

// FreeCompanyController exposes the Free Company REST API.
type FreeCompanyController struct {
	svc *census.Service
}

func NewFreeCompanyController(svc *census.Service) *FreeCompanyController {
	return &FreeCompanyController{svc: svc}
}

// List serves GET /api/v1/census/free-companies: one page of free companies plus total.
func (c *FreeCompanyController) List(w http.ResponseWriter, r *http.Request) {
	if c.svc == nil {
		writeError(w, http.StatusInternalServerError, "census service unavailable")
		return
	}
	query := r.URL.Query()

	limit := 100
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if n > 500 {
			n = 500
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

	f := contract.FreeCompanyFilter{
		World:        query.Get("world"),
		Datacenter:   query.Get("datacenter"),
		Name:         query.Get("name"),
		Tag:          query.Get("tag"),
		GrandCompany: query.Get("grand_company"),
		SortBy:       query.Get("sort_by"),
		SortOrder:    query.Get("sort_order"),
	}

	fcs, total, err := c.svc.ListFreeCompanies(r.Context(), f, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	items := make([]response.FreeCompanyListItem, 0, len(fcs))
	for i := range fcs {
		items = append(items, response.FreeCompanyListItem{
			ID:          fcs[i].ID,
			Name:        fcs[i].Name,
			World:       fcs[i].World,
			Datacenter:  fcs[i].Datacenter,
			MemberCount: fcs[i].MemberCount,
			FormedAt:    fcs[i].FormedAt,
			LastSeenAt:  fcs[i].LastSeenAt,
		})
	}

	writeJSON(w, http.StatusOK, response.PaginatedFreeCompanies{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// Get serves GET /api/v1/census/free-companies/{id}: detail for one free company.
func (c *FreeCompanyController) Get(w http.ResponseWriter, r *http.Request) {
	if c.svc == nil {
		writeError(w, http.StatusInternalServerError, "census service unavailable")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing free company id")
		return
	}

	fc, err := c.svc.FreeCompanyDetail(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if fc == nil {
		writeError(w, http.StatusNotFound, "free company not found")
		return
	}

	writeJSON(w, http.StatusOK, response.FreeCompanyDetail{
		ID:          fc.ID,
		Name:        fc.Name,
		World:       fc.World,
		Datacenter:  fc.Datacenter,
		MemberCount: fc.MemberCount,
	})
}
