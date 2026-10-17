package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	meilisearch "github.com/meilisearch/meilisearch-go"
	"github.com/statis/wahi-api/internal/config"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newTestServer(t *testing.T, rps float64, burst int) (*Server, *[]string) {
	t.Helper()

	var recordedBodies []string

	meiliMux := http.NewServeMux()
	meiliMux.HandleFunc("/indexes/addresses/search", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		recordedBodies = append(recordedBodies, string(body))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"hits": [
				{
					"address_id": 3000000,
					"road_id": 1000000,
					"full_address_number": "1/100A-120A",
					"full_road_name": "Queen Street West",
					"full_address": "1/100A-120A, Queen Street West, Auckland Central, Auckland",
					"suburb_locality": "Auckland Central",
					"town_city": "Auckland",
					"_geo": {"lat": -36.8485, "lng": 174.7633}
				}
			],
			"processingTimeMs": 2,
			"query": "Queen St 1 110"
		}`))
	})

	meiliMux.HandleFunc("/multi-search", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		recordedBodies = append(recordedBodies, string(body))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"results": [
				{
					"indexUid": "localities",
					"processingTimeMs": 1,
					"hits": [
						{
							"id": 1002,
							"name": "Ponsonby",
							"major_name": "Auckland",
							"_geo": {"lat": -36.8520, "lng": 174.7460}
						}
					]
				},
				{
					"indexUid": "streets",
					"processingTimeMs": 2,
					"hits": [
						{
							"id": "1000001_2000003",
							"road_id": 1000001,
							"full_road_name": "Ponsonby Road",
							"suburb_locality": "Ponsonby",
							"town_city": "Auckland",
							"_geo": {"lat": -36.8520, "lng": 174.7460}
						}
					]
				},
				{
					"indexUid": "addresses",
					"processingTimeMs": 3,
					"hits": [
						{
							"address_id": 3000002,
							"road_id": 1000001,
							"full_address_number": "15B",
							"full_road_name": "Ponsonby Road",
							"full_address": "15B, Ponsonby Road, Ponsonby, Auckland",
							"suburb_locality": "Ponsonby",
							"town_city": "Auckland",
							"_geo": {"lat": -36.8520, "lng": 174.7460}
						}
					]
				}
			]
		}`))
	})

	meiliMux.HandleFunc("/indexes/addresses/documents/3000000", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"address_id": 3000000,
			"road_id": 1000000,
			"full_address_number": "1/100A-120A",
			"full_road_name": "Queen Street West",
			"full_address": "1/100A-120A, Queen Street West, Auckland Central, Auckland",
			"unit": "1",
			"address_number": 100,
			"address_number_suffix": "A",
			"address_number_high": 120,
			"road_name": "Queen",
			"road_name_type": "Street",
			"road_name_suffix": "West",
			"road_name_label": "Queen St W",
			"suburb_locality": "Auckland Central",
			"town_city": "Auckland",
			"territorial_authority": "Auckland",
			"is_land": true,
			"address_lifecycle": "Current",
			"_geo": {"lat": -36.8485, "lng": 174.7633}
		}`))
	})

	meiliMux.HandleFunc("/indexes/addresses/documents/9999999", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Document 9999999 not found.","code":"document_not_found","type":"invalid_request"}`))
	})

	meiliMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"available"}`))
	})

	meiliMux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"databaseSize": 1024,
			"indexes": {
				"addresses": {"numberOfDocuments": 2430000, "isIndexing": false},
				"streets": {"numberOfDocuments": 65000, "isIndexing": false},
				"localities": {"numberOfDocuments": 3100, "isIndexing": false}
			}
		}`))
	})

	httpClient := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			meiliMux.ServeHTTP(rec, req)
			return rec.Result(), nil
		}),
	}

	cfg := &config.Config{
		Port:           "8080",
		MeiliHost:      "http://meili.local",
		MeiliAPIKey:    "test_key",
		RateLimitRPS:   rps,
		RateLimitBurst: burst,
		CORSOrigins:    []string{"*"},
	}

	meiliClient := meilisearch.New(
		cfg.MeiliHost,
		meilisearch.WithAPIKey(cfg.MeiliAPIKey),
		meilisearch.WithCustomClient(httpClient),
	)

	return NewServer(cfg, meiliClient), &recordedBodies
}

func TestAutosuggestWithStreetNumber(t *testing.T) {
	srv, bodies := newTestServer(t, 50, 100)

	req := httptest.NewRequest(http.MethodGet, "/v1/autosuggest?q=1/110+Queen+St&lat=-36.8485&lon=174.7633", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	if len(*bodies) == 0 {
		t.Fatal("expected Meilisearch search request body")
	}
	var searchReq struct {
		Filter string `json:"filter"`
		Q      string `json:"q"`
	}
	if err := json.Unmarshal([]byte((*bodies)[0]), &searchReq); err != nil {
		t.Fatalf("unmarshal search request: %v", err)
	}
	if !strings.Contains(searchReq.Filter, "address_number <= 110 AND address_number_high >= 110") {
		t.Errorf("expected range filter in Meilisearch request filter, got %q", searchReq.Filter)
	}
	if !strings.Contains(searchReq.Filter, "_geoRadius(-36.848500, 174.763300, 50000)") {
		t.Errorf("expected _geoRadius filter in Meilisearch request filter, got %q", searchReq.Filter)
	}

	var resp struct {
		Results []struct {
			ID                int    `json:"id"`
			Type              string `json:"type"`
			FullAddressNumber string `json:"full_address_number"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].ID != 3000000 || resp.Results[0].Type != "address" {
		t.Errorf("unexpected results: %+v", resp.Results)
	}
}

func TestAutosuggestWithoutStreetNumberMultiSearch(t *testing.T) {
	srv, _ := newTestServer(t, 50, 100)

	req := httptest.NewRequest(http.MethodGet, "/v1/autosuggest?q=Ponsonby", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Results []struct {
			ID   int    `json:"id"`
			Type string `json:"type"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("expected 3 merged results (locality, street, address), got %d", len(resp.Results))
	}
	if resp.Results[0].Type != "locality" || resp.Results[1].Type != "street" || resp.Results[2].Type != "address" {
		t.Errorf("unexpected result types ordering: %+v", resp.Results)
	}
}

func TestGetAddressByID(t *testing.T) {
	srv, _ := newTestServer(t, 50, 100)

	t.Run("existing address", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/addresses/3000000", nil)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"address_number_high":120`) {
			t.Errorf("expected address_number_high in response, got %s", rec.Body.String())
		}
	})

	t.Run("not found address", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/addresses/9999999", nil)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 Not Found, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestReverseGeocode(t *testing.T) {
	srv, bodies := newTestServer(t, 50, 100)

	req := httptest.NewRequest(http.MethodGet, "/v1/reverse?lat=-36.8485&lon=174.7633&radius_m=150&limit=5", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*bodies) == 0 || !strings.Contains((*bodies)[0], "_geoRadius(-36.848500, 174.763300, 150)") {
		t.Errorf("expected _geoRadius(.., 150) in Meilisearch request, got %v", *bodies)
	}
}

func TestHealthzAndOpenAPI(t *testing.T) {
	srv, _ := newTestServer(t, 50, 100)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on /healthz, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"addresses":2430000`) {
		t.Errorf("expected document counts in /healthz, got %s", rec.Body.String())
	}

	openAPIReq := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	openAPIRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(openAPIRec, openAPIReq)
	if openAPIRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on /openapi.json, got %d", openAPIRec.Code)
	}
	if !strings.Contains(openAPIRec.Body.String(), `"/v1/autosuggest"`) {
		t.Errorf("expected /v1/autosuggest in OpenAPI spec")
	}
}

func TestRateLimitingAndCORS(t *testing.T) {
	srv, _ := newTestServer(t, 0.01, 1)

	// First request succeeds
	req1 := httptest.NewRequest(http.MethodGet, "/v1/autosuggest?q=Queen", nil)
	req1.RemoteAddr = "203.0.113.10:12345"
	rec1 := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("expected first request 200 OK, got %d", rec1.Code)
	}
	if rec1.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected CORS header *, got %q", rec1.Header().Get("Access-Control-Allow-Origin"))
	}

	// Second immediate request from same IP exceeds burst=1
	req2 := httptest.NewRequest(http.MethodGet, "/v1/autosuggest?q=Queen", nil)
	req2.RemoteAddr = "203.0.113.10:12345"
	rec2 := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second request 429 Too Many Requests, got %d", rec2.Code)
	}
}

func TestAccessTokenAuthAndRotation(t *testing.T) {
	srv, _ := newTestServer(t, 50, 100)
	srv.UpdateAccessTokens([]string{"token_v1"})

	// 1. Missing token -> 401
	reqMissing := httptest.NewRequest(http.MethodGet, "/v1/autosuggest?q=Queen", nil)
	recMissing := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recMissing, reqMissing)
	if recMissing.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", recMissing.Code)
	}

	// 2. Invalid token -> 401
	reqInvalid := httptest.NewRequest(http.MethodGet, "/v1/autosuggest?q=Queen", nil)
	reqInvalid.Header.Set("Authorization", "Bearer wrong_token")
	recInvalid := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recInvalid, reqInvalid)
	if recInvalid.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with invalid token, got %d", recInvalid.Code)
	}

	// 3. Valid Bearer token -> 200
	reqBearer := httptest.NewRequest(http.MethodGet, "/v1/autosuggest?q=Queen", nil)
	reqBearer.Header.Set("Authorization", "Bearer token_v1")
	recBearer := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recBearer, reqBearer)
	if recBearer.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid Bearer token, got %d", recBearer.Code)
	}

	// 4. Rotate tokens: add token_v2 alongside token_v1
	srv.UpdateAccessTokens([]string{"token_v2", "token_v1"})

	// Both X-API-Key: token_v2 and ?api_key=token_v1 work during rotation window
	reqHeaderKey := httptest.NewRequest(http.MethodGet, "/v1/autosuggest?q=Queen", nil)
	reqHeaderKey.Header.Set("X-API-Key", "token_v2")
	recHeaderKey := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recHeaderKey, reqHeaderKey)
	if recHeaderKey.Code != http.StatusOK {
		t.Fatalf("expected 200 with X-API-Key token_v2, got %d", recHeaderKey.Code)
	}

	reqQueryKey := httptest.NewRequest(http.MethodGet, "/v1/autosuggest?q=Queen&api_key=token_v1", nil)
	recQueryKey := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recQueryKey, reqQueryKey)
	if recQueryKey.Code != http.StatusOK {
		t.Fatalf("expected 200 with ?api_key=token_v1 during rotation, got %d", recQueryKey.Code)
	}

	// 5. Complete rotation: retire token_v1
	srv.UpdateAccessTokens([]string{"token_v2"})
	recRetired := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recRetired, reqQueryKey)
	if recRetired.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for retired token_v1, got %d", recRetired.Code)
	}

	// 6. Healthz remains accessible without token
	reqHealth := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recHealth := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recHealth, reqHealth)
	if recHealth.Code != http.StatusOK {
		t.Fatalf("expected /healthz 200 without token, got %d", recHealth.Code)
	}
}

func TestCORSStrictAndWildcardSubdomains(t *testing.T) {
	srv, _ := newTestServer(t, 50, 100)
	srv.Config.CORSOrigins = []string{"https://example.com", "https://*.example.co.nz"}
	srv.Config.CORSStrict = true
	// Rebuild handler with updated CORS config
	srv = NewServer(srv.Config, srv.Meili)

	// Allowed wildcard subdomain
	reqAllowed := httptest.NewRequest(http.MethodGet, "/v1/autosuggest?q=Queen", nil)
	reqAllowed.Header.Set("Origin", "https://checkout.example.co.nz")
	recAllowed := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recAllowed, reqAllowed)
	if recAllowed.Code != http.StatusOK {
		t.Fatalf("expected 200 for allowed wildcard subdomain, got %d", recAllowed.Code)
	}
	if got := recAllowed.Header().Get("Access-Control-Allow-Origin"); got != "https://checkout.example.co.nz" {
		t.Errorf("expected Access-Control-Allow-Origin 'https://checkout.example.co.nz', got %q", got)
	}

	// Disallowed origin with CORS_STRICT=true -> 403 Forbidden
	reqBlocked := httptest.NewRequest(http.MethodGet, "/v1/autosuggest?q=Queen", nil)
	reqBlocked.Header.Set("Origin", "https://evil.example.org")
	recBlocked := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recBlocked, reqBlocked)
	if recBlocked.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for disallowed Origin under CORS_STRICT, got %d", recBlocked.Code)
	}
}
