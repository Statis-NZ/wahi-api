package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	huma "github.com/danielgtaylor/huma/v2"
	meilisearch "github.com/meilisearch/meilisearch-go"
	"github.com/statis/wahi-api/internal/ingest"
	"github.com/statis/wahi-api/internal/models"
)

// AutosuggestInput defines query parameters for GET /v1/autosuggest.
type AutosuggestInput struct {
	Q               string  `query:"q" minLength:"2" required:"true" doc:"Search string (minimum 2 characters)" example:"1/100A Queen St"`
	Limit           int     `query:"limit" default:"10" minimum:"1" maximum:"25" doc:"Maximum number of results to return"`
	Type            string  `query:"type" default:"all" enum:"address,street,locality,all" doc:"Result type filter"`
	Lat             float64 `query:"lat" required:"false" doc:"Optional latitude for proximity boosting"`
	Lon             float64 `query:"lon" required:"false" doc:"Optional longitude for proximity boosting"`
	TownCity        string  `query:"town_city" required:"false" doc:"Filter results to a specific town or city"`
	IncludeProposed bool    `query:"include_proposed" default:"false" doc:"Include Proposed addresses in addition to Current addresses"`
}

// AutosuggestOutput wraps the response body for GET /v1/autosuggest and GET /v1/reverse.
type AutosuggestOutput struct {
	Body struct {
		Results     []models.AutosuggestItem `json:"results" doc:"Matched address, street, or locality results"`
		QueryTimeMs int64                    `json:"query_time_ms" doc:"Search execution time in milliseconds" example:"3"`
	}
}

// AddressByIDInput defines the path parameter for GET /v1/addresses/{address_id}.
type AddressByIDInput struct {
	AddressID int `path:"address_id" minimum:"1" doc:"LINZ primary address_id" example:"3000000"`
}

// AddressByIDOutput wraps the response body for GET /v1/addresses/{address_id}.
type AddressByIDOutput struct {
	Body models.AddressDetailResponse
}

// ReverseInput defines query parameters for GET /v1/reverse.
type ReverseInput struct {
	Lat     float64 `query:"lat" required:"true" minimum:"-90" maximum:"90" doc:"Latitude in decimal degrees" example:"-36.8485"`
	Lon     float64 `query:"lon" required:"true" minimum:"-180" maximum:"180" doc:"Longitude in decimal degrees" example:"174.7633"`
	RadiusM int     `query:"radius_m" default:"100" minimum:"1" maximum:"1000" doc:"Search radius in meters (max 1000)"`
	Limit   int     `query:"limit" default:"5" minimum:"1" maximum:"25" doc:"Maximum number of nearest addresses to return"`
}

// HealthzOutput wraps the response body for GET /healthz.
type HealthzOutput struct {
	Body struct {
		Status            string           `json:"status" doc:"Service health status" example:"ok"`
		MeilisearchStatus string           `json:"meilisearch_status" doc:"Meilisearch health status" example:"available"`
		Indexes           map[string]int64 `json:"indexes" doc:"Document counts for addresses, streets, and localities"`
	}
}

func (s *Server) registerRoutes() {
	huma.Register(s.Huma, huma.Operation{
		OperationID: "autosuggest",
		Method:      http.MethodGet,
		Path:        "/v1/autosuggest",
		Summary:     "Address, street, and locality autosuggest",
		Description: "Provides fast typeahead suggestions across New Zealand addresses, streets, and localities with unit/range pre-parsing and optional geo-biasing.",
		Tags:        []string{"Search"},
	}, s.handleAutosuggest)

	huma.Register(s.Huma, huma.Operation{
		OperationID: "get-address",
		Method:      http.MethodGet,
		Path:        "/v1/addresses/{address_id}",
		Summary:     "Get address by LINZ address_id",
		Description: "Fetches the full decomposed LINZ address document and coordinates by its primary key.",
		Tags:        []string{"Addresses"},
	}, s.handleGetAddress)

	huma.Register(s.Huma, huma.Operation{
		OperationID: "reverse-geocode",
		Method:      http.MethodGet,
		Path:        "/v1/reverse",
		Summary:     "Reverse geocode coordinates",
		Description: "Finds the nearest New Zealand street addresses within a given radius of a latitude/longitude coordinate.",
		Tags:        []string{"Geocoding"},
	}, s.handleReverse)

	huma.Register(s.Huma, huma.Operation{
		OperationID: "healthz",
		Method:      http.MethodGet,
		Path:        "/healthz",
		Summary:     "Health and index statistics check",
		Description: "Checks Meilisearch health status and returns document counts for addresses, streets, and localities.",
		Tags:        []string{"System"},
	}, s.handleHealthz)
}

func (s *Server) handleAutosuggest(ctx context.Context, input *AutosuggestInput) (*AutosuggestOutput, error) {
	start := time.Now()
	parsed := ParseAddressQuery(input.Q)

	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 25 {
		limit = 25
	}

	reqType := strings.ToLower(strings.TrimSpace(input.Type))
	if reqType == "" {
		reqType = "all"
	}

	hasGeo := input.Lat != 0 || input.Lon != 0
	var geoSort []string
	if hasGeo && len(strings.TrimSpace(input.Q)) >= 3 {
		geoSort = []string{fmt.Sprintf("_geoPoint(%f, %f):asc", input.Lat, input.Lon)}
	}

	out := &AutosuggestOutput{}
	out.Body.Results = make([]models.AutosuggestItem, 0)

	// Case A: Query starts with a street number (or caller specifically requested only addresses/streets/localities)
	if parsed.HasStreetNumber || reqType == "address" {
		addrFilter := buildAddressFilter(parsed, input.TownCity, input.IncludeProposed, hasGeo, input.Lat, input.Lon)
		searchQuery := buildAddressSearchText(parsed)

		resp, err := s.Meili.Index(ingest.IndexAddresses).SearchWithContext(ctx, searchQuery, &meilisearch.SearchRequest{
			Limit:  int64(limit),
			Filter: addrFilter,
			Sort:   geoSort,
		})
		if err != nil {
			return nil, huma.Error502BadGateway("Meilisearch address search failed", err)
		}

		items, err := decodeAddressHits(resp.Hits)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to decode address hits", err)
		}
		out.Body.Results = items
		out.Body.QueryTimeMs = maxInt64(resp.ProcessingTimeMs, time.Since(start).Milliseconds())
		return out, nil
	}

	if reqType == "street" {
		streetFilter := buildStreetFilter(input.TownCity, hasGeo, input.Lat, input.Lon)
		resp, err := s.Meili.Index(ingest.IndexStreets).SearchWithContext(ctx, parsed.Raw, &meilisearch.SearchRequest{
			Limit:  int64(limit),
			Filter: streetFilter,
			Sort:   geoSort,
		})
		if err != nil {
			return nil, huma.Error502BadGateway("Meilisearch street search failed", err)
		}
		items, err := decodeStreetHits(resp.Hits)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to decode street hits", err)
		}
		out.Body.Results = items
		out.Body.QueryTimeMs = maxInt64(resp.ProcessingTimeMs, time.Since(start).Milliseconds())
		return out, nil
	}

	if reqType == "locality" {
		locFilter := buildLocalityFilter(input.TownCity, hasGeo, input.Lat, input.Lon)
		resp, err := s.Meili.Index(ingest.IndexLocalities).SearchWithContext(ctx, parsed.Raw, &meilisearch.SearchRequest{
			Limit:  int64(limit),
			Filter: locFilter,
			Sort:   geoSort,
		})
		if err != nil {
			return nil, huma.Error502BadGateway("Meilisearch locality search failed", err)
		}
		items, err := decodeLocalityHits(resp.Hits)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to decode locality hits", err)
		}
		out.Body.Results = items
		out.Body.QueryTimeMs = maxInt64(resp.ProcessingTimeMs, time.Since(start).Milliseconds())
		return out, nil
	}

	// Case B: Query has NO leading street number and type == "all"
	// Execute a single MultiSearch call across localities (limit 3), streets (limit 5), and addresses (limit `limit`).
	locFilter := buildLocalityFilter(input.TownCity, hasGeo, input.Lat, input.Lon)
	streetFilter := buildStreetFilter(input.TownCity, hasGeo, input.Lat, input.Lon)
	addrFilter := buildAddressFilter(parsed, input.TownCity, input.IncludeProposed, hasGeo, input.Lat, input.Lon)

	multiResp, err := s.Meili.MultiSearchWithContext(ctx, &meilisearch.MultiSearchRequest{
		Queries: []*meilisearch.SearchRequest{
			{
				IndexUID: ingest.IndexLocalities,
				Query:    parsed.Raw,
				Limit:    3,
				Filter:   locFilter,
				Sort:     geoSort,
			},
			{
				IndexUID: ingest.IndexStreets,
				Query:    parsed.Raw,
				Limit:    5,
				Filter:   streetFilter,
				Sort:     geoSort,
			},
			{
				IndexUID: ingest.IndexAddresses,
				Query:    parsed.Raw,
				Limit:    int64(limit),
				Filter:   addrFilter,
				Sort:     geoSort,
			},
		},
	})
	if err != nil {
		return nil, huma.Error502BadGateway("Meilisearch multi-search failed", err)
	}

	seen := make(map[string]struct{}, limit)
	var maxProcMs int64

	for _, res := range multiResp.Results {
		if res.ProcessingTimeMs > maxProcMs {
			maxProcMs = res.ProcessingTimeMs
		}
		var items []models.AutosuggestItem
		switch res.IndexUID {
		case ingest.IndexLocalities:
			items, _ = decodeLocalityHits(res.Hits)
		case ingest.IndexStreets:
			items, _ = decodeStreetHits(res.Hits)
		case ingest.IndexAddresses:
			items, _ = decodeAddressHits(res.Hits)
		}
		for _, item := range items {
			key := fmt.Sprintf("%s:%d:%s", item.Type, item.ID, strings.ToLower(item.FullAddress))
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out.Body.Results = append(out.Body.Results, item)
			if len(out.Body.Results) >= limit {
				break
			}
		}
		if len(out.Body.Results) >= limit {
			break
		}
	}

	out.Body.QueryTimeMs = maxInt64(maxProcMs, time.Since(start).Milliseconds())
	return out, nil
}

func (s *Server) handleGetAddress(ctx context.Context, input *AddressByIDInput) (*AddressByIDOutput, error) {
	idStr := strconv.Itoa(input.AddressID)
	var doc models.AddressDocument

	err := s.Meili.Index(ingest.IndexAddresses).GetDocumentWithContext(ctx, idStr, nil, &doc)
	if err != nil {
		var meiliErr *meilisearch.Error
		if errors.As(err, &meiliErr) && meiliErr.StatusCode == http.StatusNotFound {
			return nil, huma.Error404NotFound(fmt.Sprintf("Address %d not found", input.AddressID))
		}
		if strings.Contains(strings.ToLower(err.Error()), "404") || strings.Contains(strings.ToLower(err.Error()), "not_found") {
			return nil, huma.Error404NotFound(fmt.Sprintf("Address %d not found", input.AddressID))
		}
		return nil, huma.Error502BadGateway("Failed to fetch address from Meilisearch", err)
	}

	var loc *models.Location
	if doc.Geo != nil {
		loc = &models.Location{
			Lat: doc.Geo.Lat,
			Lon: doc.Geo.Lng,
		}
	}

	return &AddressByIDOutput{
		Body: models.AddressDetailResponse{
			AddressID:            doc.AddressID,
			RoadID:               doc.RoadID,
			FullAddress:          doc.FullAddress,
			FullAddressNumber:    doc.FullAddressNumber,
			FullRoadName:         doc.FullRoadName,
			Unit:                 doc.Unit,
			AddressNumber:        doc.AddressNumber,
			AddressNumberSuffix:  doc.AddressNumberSuffix,
			AddressNumberHigh:    doc.AddressNumberHigh,
			RoadName:             doc.RoadName,
			RoadNameType:         doc.RoadNameType,
			RoadNameSuffix:       doc.RoadNameSuffix,
			RoadNameLabel:        doc.RoadNameLabel,
			SecondaryRoadName:    doc.SecondaryRoadName,
			SuburbLocality:       doc.SuburbLocality,
			TownCity:             doc.TownCity,
			TerritorialAuthority: doc.TerritorialAuthority,
			IsLand:               doc.IsLand,
			AddressLifecycle:     doc.AddressLifecycle,
			Location:             loc,
		},
	}, nil
}

func (s *Server) handleReverse(ctx context.Context, input *ReverseInput) (*AutosuggestOutput, error) {
	start := time.Now()

	radius := input.RadiusM
	if radius <= 0 {
		radius = 100
	}
	if radius > 1000 {
		radius = 1000
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 25 {
		limit = 25
	}

	filter := fmt.Sprintf("_geoRadius(%f, %f, %d)", input.Lat, input.Lon, radius)
	sort := []string{fmt.Sprintf("_geoPoint(%f, %f):asc", input.Lat, input.Lon)}

	resp, err := s.Meili.Index(ingest.IndexAddresses).SearchWithContext(ctx, "", &meilisearch.SearchRequest{
		Limit:  int64(limit),
		Filter: filter,
		Sort:   sort,
	})
	if err != nil {
		return nil, huma.Error502BadGateway("Meilisearch reverse geocode query failed", err)
	}

	items, err := decodeAddressHits(resp.Hits)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to decode reverse geocode hits", err)
	}

	out := &AutosuggestOutput{}
	out.Body.Results = items
	out.Body.QueryTimeMs = maxInt64(resp.ProcessingTimeMs, time.Since(start).Milliseconds())
	return out, nil
}

func (s *Server) handleHealthz(ctx context.Context, _ *struct{}) (*HealthzOutput, error) {
	health, err := s.Meili.HealthWithContext(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("Meilisearch is unreachable", err)
	}

	stats, err := s.Meili.GetStatsWithContext(ctx, nil)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("Failed to retrieve Meilisearch index stats", err)
	}

	counts := map[string]int64{
		ingest.IndexAddresses:  0,
		ingest.IndexStreets:    0,
		ingest.IndexLocalities: 0,
	}
	if stats != nil && stats.Indexes != nil {
		for name := range counts {
			if idxStat, ok := stats.Indexes[name]; ok {
				counts[name] = idxStat.NumberOfDocuments
			}
		}
	}

	out := &HealthzOutput{}
	out.Body.Status = "ok"
	out.Body.MeilisearchStatus = health.Status
	out.Body.Indexes = counts
	return out, nil
}

// buildAddressSearchText orders the street query before the unit/number token so Meilisearch's
// default "last" word-matching strategy ranks exact full_address_number matches first while still
// returning ranged addresses (e.g., 100-120 for query "110 Queen St") via the numeric filter.
func buildAddressSearchText(parsed ParsedQuery) string {
	if !parsed.HasStreetNumber || parsed.StreetQuery == "" {
		return parsed.Raw
	}
	var parts []string
	parts = append(parts, parsed.StreetQuery)
	if parsed.Unit != "" {
		parts = append(parts, parsed.Unit)
	}
	parts = append(parts, fmt.Sprintf("%d%s", parsed.Number, parsed.NumberSuffix))
	return strings.Join(parts, " ")
}

func buildAddressFilter(parsed ParsedQuery, townCity string, includeProposed, hasGeo bool, lat, lon float64) string {
	var clauses []string
	if numFilter := parsed.BuildAddressNumberFilter(); numFilter != "" {
		clauses = append(clauses, numFilter)
	}
	if !includeProposed {
		clauses = append(clauses, `address_lifecycle = "Current"`)
	}
	if strings.TrimSpace(townCity) != "" {
		clauses = append(clauses, fmt.Sprintf("town_city = %q", strings.TrimSpace(townCity)))
	}
	if hasGeo {
		clauses = append(clauses, fmt.Sprintf("_geoRadius(%f, %f, 50000)", lat, lon))
	}
	return strings.Join(clauses, " AND ")
}

func buildStreetFilter(townCity string, hasGeo bool, lat, lon float64) string {
	var clauses []string
	if strings.TrimSpace(townCity) != "" {
		clauses = append(clauses, fmt.Sprintf("town_city = %q", strings.TrimSpace(townCity)))
	}
	if hasGeo {
		clauses = append(clauses, fmt.Sprintf("_geoRadius(%f, %f, 50000)", lat, lon))
	}
	return strings.Join(clauses, " AND ")
}

func buildLocalityFilter(townCity string, hasGeo bool, lat, lon float64) string {
	var clauses []string
	if strings.TrimSpace(townCity) != "" {
		clauses = append(clauses, fmt.Sprintf("major_name = %q", strings.TrimSpace(townCity)))
	}
	if hasGeo {
		clauses = append(clauses, fmt.Sprintf("_geoRadius(%f, %f, 50000)", lat, lon))
	}
	return strings.Join(clauses, " AND ")
}

func decodeAddressHits(hits meilisearch.Hits) ([]models.AutosuggestItem, error) {
	var docs []models.AddressDocument
	if err := hits.DecodeInto(&docs); err != nil {
		return nil, err
	}
	items := make([]models.AutosuggestItem, 0, len(docs))
	for _, d := range docs {
		var loc *models.Location
		if d.Geo != nil {
			loc = &models.Location{Lat: d.Geo.Lat, Lon: d.Geo.Lng}
		}
		items = append(items, models.AutosuggestItem{
			ID:                d.AddressID,
			Type:              "address",
			FullAddress:       d.FullAddress,
			FullAddressNumber: d.FullAddressNumber,
			FullRoadName:      d.FullRoadName,
			SuburbLocality:    d.SuburbLocality,
			TownCity:          d.TownCity,
			Location:          loc,
		})
	}
	return items, nil
}

func decodeStreetHits(hits meilisearch.Hits) ([]models.AutosuggestItem, error) {
	var docs []models.StreetDocument
	if err := hits.DecodeInto(&docs); err != nil {
		return nil, err
	}
	items := make([]models.AutosuggestItem, 0, len(docs))
	for _, d := range docs {
		var loc *models.Location
		if d.Geo != nil {
			loc = &models.Location{Lat: d.Geo.Lat, Lon: d.Geo.Lng}
		}
		parts := []string{d.FullRoadName}
		if d.SuburbLocality != "" {
			parts = append(parts, d.SuburbLocality)
		}
		if d.TownCity != "" && !strings.EqualFold(d.TownCity, d.SuburbLocality) {
			parts = append(parts, d.TownCity)
		}
		items = append(items, models.AutosuggestItem{
			ID:             d.RoadID,
			Type:           "street",
			FullAddress:    strings.Join(parts, ", "),
			FullRoadName:   d.FullRoadName,
			SuburbLocality: d.SuburbLocality,
			TownCity:       d.TownCity,
			Location:       loc,
		})
	}
	return items, nil
}

func decodeLocalityHits(hits meilisearch.Hits) ([]models.AutosuggestItem, error) {
	var docs []models.LocalityDocument
	if err := hits.DecodeInto(&docs); err != nil {
		return nil, err
	}
	items := make([]models.AutosuggestItem, 0, len(docs))
	for _, d := range docs {
		var loc *models.Location
		if d.Geo != nil {
			loc = &models.Location{Lat: d.Geo.Lat, Lon: d.Geo.Lng}
		}
		label := d.Name
		if d.MajorName != "" && !strings.EqualFold(d.MajorName, d.Name) {
			label = fmt.Sprintf("%s, %s", d.Name, d.MajorName)
		}
		items = append(items, models.AutosuggestItem{
			ID:             d.ID,
			Type:           "locality",
			FullAddress:    label,
			SuburbLocality: d.Name,
			TownCity:       d.MajorName,
			Location:       loc,
		})
	}
	return items, nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
