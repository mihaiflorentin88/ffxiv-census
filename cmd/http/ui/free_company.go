package ui

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// FreeCompanyListViewData holds the data for the Free Company directory page.
type FreeCompanyListViewData struct {
	Title         string
	Query         string
	World         string
	GrandCompany  string
	SortBy        string
	SortOrder     string
	FreeCompanies []FreeCompanyRow
	TotalCount    int64
	CurrentPage   int
	TotalPages    int
	HasPrev       bool
	HasNext       bool
	PrevPage      int
	NextPage      int
}

// FreeCompanyRow represents one Free Company in the list view.
type FreeCompanyRow struct {
	ID          string
	Name        string
	World       string
	Datacenter  string
	MemberCount uint32
	FormedAt    *time.Time
	LastSeenAt  time.Time
}

// FreeCompanyProfileViewData holds data for the Free Company detail page.
type FreeCompanyProfileViewData struct {
	FreeCompany contract.FreeCompanyRecord
	Members     []CharacterRow
	TotalCount  int64
}

// FreeCompanyList handles GET /ui/free-companies.
func (c *UIController) FreeCompanyList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query()

	page := 1
	if pStr := query.Get("page"); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			page = p
		}
	}

	limit := 30
	offset := (page - 1) * limit

	qName := strings.TrimSpace(query.Get("q"))
	if qName == "" {
		qName = strings.TrimSpace(query.Get("name"))
	}
	world := strings.TrimSpace(query.Get("world"))
	grandCompany := strings.TrimSpace(query.Get("grand_company"))
	sortBy := query.Get("sort_by")
	sortOrder := query.Get("sort_order")

	filter := contract.FreeCompanyFilter{
		Name:         qName,
		World:        world,
		GrandCompany: grandCompany,
		SortBy:       sortBy,
		SortOrder:    sortOrder,
	}

	var fcs []contract.FreeCompanyRecord
	var total int64
	var err error

	if c.svc != nil {
		fcs, total, err = c.svc.ListFreeCompanies(ctx, filter, limit, offset)
		if err != nil {
			logging.Error("ui.free_company.list", err.Error())
		}
	}

	totalPages := int((total + int64(limit) - 1) / int64(limit))
	if totalPages == 0 {
		totalPages = 1
	}

	var rows []FreeCompanyRow
	for _, fc := range fcs {
		rows = append(rows, FreeCompanyRow{
			ID:          fc.ID,
			Name:        fc.Name,
			World:       fc.World,
			Datacenter:  fc.Datacenter,
			MemberCount: fc.MemberCount,
			FormedAt:    fc.FormedAt,
			LastSeenAt:  fc.LastSeenAt,
		})
	}

	title := "Free Company Directory"
	if qName != "" {
		title = fmt.Sprintf("Search: %q", qName)
	}

	c.render(w, "templates/free_companies_list.html", PageData{
		Title:       title,
		ActiveNav:   "free-companies",
		SearchQuery: qName,
		Data: FreeCompanyListViewData{
			Title:         title,
			Query:         qName,
			World:         world,
			GrandCompany:  grandCompany,
			SortBy:        sortBy,
			SortOrder:     sortOrder,
			FreeCompanies: rows,
			TotalCount:    total,
			CurrentPage:   page,
			TotalPages:    totalPages,
			HasPrev:       page > 1,
			HasNext:       page < totalPages,
			PrevPage:      page - 1,
			NextPage:      page + 1,
		},
	})
}

// FreeCompanyDetail handles GET /ui/free-companies/{id}.
func (c *UIController) FreeCompanyDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		id = r.URL.Query().Get("id")
	}

	if id == "" {
		http.Error(w, "Missing Free Company ID", http.StatusBadRequest)
		return
	}

	var fc *contract.FreeCompanyRecord
	var err error
	if c.svc != nil {
		fc, err = c.svc.FreeCompanyDetail(ctx, id)
		if err != nil {
			logging.Error("ui.free_company.detail", err.Error())
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
	}

	if fc == nil {
		w.WriteHeader(http.StatusNotFound)
		c.render(w, "templates/free_company_detail.html", PageData{
			Title:     "Free Company Not Found",
			ActiveNav: "free-companies",
			Data: FreeCompanyProfileViewData{
				FreeCompany: contract.FreeCompanyRecord{ID: id, Name: "Unknown Free Company"},
			},
		})
		return
	}

	// Fetch members belonging to this Free Company
	var members []contract.CharacterRecord
	var totalMembers int64
	if c.svc != nil {
		members, totalMembers, err = c.svc.ListCharacters(ctx, contract.CharacterFilter{FreeCompanyID: id}, 100, 0)
		if err != nil {
			logging.Error("ui.free_company.members", err.Error())
		}
	}

	var memberRows []CharacterRow
	for _, ch := range members {
		isActive := false
		if ch.LatestAchievementAt != nil && c.svc != nil {
			isActive = c.svc.IsActive(*ch.LatestAchievementAt)
		}
		fcName := fc.Name
		memberRows = append(memberRows, CharacterRow{
			ID:              ch.ID,
			Name:            ch.Name,
			World:           ch.World,
			Datacenter:      ch.Datacenter,
			Region:          ch.Region,
			Race:            ch.Race,
			Tribe:           ch.Tribe,
			GrandCompany:    ch.GrandCompany,
			FreeCompanyName: fcName,
			IsActive:        isActive,
			DeletedAt:       ch.DeletedAt,
		})
	}

	c.render(w, "templates/free_company_detail.html", PageData{
		Title:     fc.Name,
		ActiveNav: "free-companies",
		Data: FreeCompanyProfileViewData{
			FreeCompany: *fc,
			Members:     memberRows,
			TotalCount:  totalMembers,
		},
	})
}
