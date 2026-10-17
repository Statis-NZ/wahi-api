package ingest

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/meilisearch/meilisearch-go"
	"github.com/statis/wahi-api/internal/config"
	"github.com/statis/wahi-api/internal/models"
)

func TestParseWKTToGeo(t *testing.T) {
	t.Run("EPSG:4167 Point", func(t *testing.T) {
		geo, err := ParseWKTToGeo("POINT (174.7633 -36.8485)")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if geo == nil {
			t.Fatal("expected non-nil geo")
		}
		if math.Abs(geo.Lat-(-36.8485)) > 1e-4 || math.Abs(geo.Lng-174.7633) > 1e-4 {
			t.Errorf("got (%f, %f), want (-36.8485, 174.7633)", geo.Lat, geo.Lng)
		}
	})

	t.Run("EWKT EPSG:2193 NZTM2000 Polygon centroid", func(t *testing.T) {
		// Auckland Central approximate NZTM2000 box around (1757000 E, 5920700 N) -> ~(-36.848, 174.763)
		wktStr := "SRID=2193;POLYGON ((1756500 5920200, 1757500 5920200, 1757500 5921200, 1756500 5921200, 1756500 5920200))"
		geo, err := ParseWKTToGeo(wktStr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if geo == nil {
			t.Fatal("expected non-nil geo")
		}
		if geo.Lat > -36.7 || geo.Lat < -37.0 || geo.Lng < 174.6 || geo.Lng > 174.9 {
			t.Errorf("unexpected NZTM2000 conversion result: lat=%f, lng=%f", geo.Lat, geo.Lng)
		}
	})

	t.Run("EPSG:4167 LineString midpoint", func(t *testing.T) {
		wktStr := "LINESTRING (174.7630 -36.8480, 174.7633 -36.8485, 174.7636 -36.8490)"
		geo, err := ParseWKTToGeo(wktStr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if math.Abs(geo.Lat-(-36.8485)) > 1e-4 || math.Abs(geo.Lng-174.7633) > 1e-4 {
			t.Errorf("got (%f, %f), want (-36.8485, 174.7633)", geo.Lat, geo.Lng)
		}
	})
}

func TestETLJoinAndStreamFixtures(t *testing.T) {
	suburbsFile, err := os.Open(filepath.Join("..", "..", "testdata", "suburbs.csv"))
	if err != nil {
		t.Fatalf("open suburbs.csv: %v", err)
	}
	defer suburbsFile.Close()

	localities, suburbMap, err := LoadSuburbsCSV(suburbsFile)
	if err != nil {
		t.Fatalf("LoadSuburbsCSV: %v", err)
	}
	if len(localities) != 5 {
		t.Fatalf("expected 5 localities, got %d", len(localities))
	}
	aklMeta, ok := suburbMap["auckland central"]
	if !ok {
		t.Fatal("expected 'auckland central' in suburbMap")
	}
	if aklMeta.PopulationEstimate != 38900 || !strings.Contains(aklMeta.AdditionalName, "Auckland CBD") {
		t.Errorf("unexpected aklMeta: %+v", aklMeta)
	}

	roadsFile, err := os.Open(filepath.Join("..", "..", "testdata", "roads.csv"))
	if err != nil {
		t.Fatalf("open roads.csv: %v", err)
	}
	defer roadsFile.Close()

	streets, roadMap, err := LoadRoadSectionsCSV(roadsFile, suburbMap)
	if err != nil {
		t.Fatalf("LoadRoadSectionsCSV: %v", err)
	}
	// 5 rows in roads.csv, but 2 share road_id=1000000 + Auckland Central + Auckland -> 4 unique streets
	if len(streets) != 4 {
		t.Fatalf("expected 4 deduplicated streets, got %d", len(streets))
	}
	sh45Alias, ok := roadMap[1000002]
	if !ok {
		t.Fatal("expected road_id 1000002 in roadMap")
	}
	if sh45Alias.SecondaryRoadName != "State Highway 45" || sh45Alias.TertiaryRoadName != "Surf Highway" {
		t.Errorf("unexpected road aliases for 1000002: %+v", sh45Alias)
	}

	addrFile, err := os.Open(filepath.Join("..", "..", "testdata", "addresses.csv"))
	if err != nil {
		t.Fatalf("open addresses.csv: %v", err)
	}
	defer addrFile.Close()

	var allAddresses []models.AddressDocument
	batches := 0
	total, err := StreamAddressesCSV(addrFile, suburbMap, roadMap, 2, func(batch []models.AddressDocument) error {
		batches++
		allAddresses = append(allAddresses, batch...)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamAddressesCSV: %v", err)
	}
	if total != 5 || len(allAddresses) != 5 {
		t.Fatalf("expected 5 addresses, got total=%d len=%d", total, len(allAddresses))
	}
	if batches != 3 { // 2 + 2 + 1
		t.Errorf("expected 3 batches of size <= 2, got %d", batches)
	}

	first := allAddresses[0]
	if first.AddressID != 3000000 {
		t.Errorf("expected AddressID 3000000, got %d", first.AddressID)
	}
	if first.RoadNameLabel != "Queen St W" {
		t.Errorf("expected enriched RoadNameLabel 'Queen St W', got %q", first.RoadNameLabel)
	}
	if first.PopulationEstimate != 38900 {
		t.Errorf("expected enriched PopulationEstimate 38900, got %d", first.PopulationEstimate)
	}
	if first.SuburbAliases != "Auckland CBD, City Centre" {
		t.Errorf("expected enriched SuburbAliases 'Auckland CBD, City Centre', got %q", first.SuburbAliases)
	}
	if first.AddressNumberHigh == nil || *first.AddressNumberHigh != 120 {
		t.Errorf("expected AddressNumberHigh=120, got %v", first.AddressNumberHigh)
	}
}

func TestIndexSettingsNoWildcard(t *testing.T) {
	settingsList := []*meilisearch.Settings{
		AddressesIndexSettings(),
		StreetsIndexSettings(),
		LocalitiesIndexSettings(),
	}
	for _, s := range settingsList {
		for _, attr := range s.SearchableAttributes {
			if attr == "*" {
				t.Errorf("wildcard '*' is prohibited in SearchableAttributes")
			}
		}
		for _, attr := range s.FilterableAttributes {
			if attr == "*" {
				t.Errorf("wildcard '*' is prohibited in FilterableAttributes")
			}
		}
		if len(s.Synonyms) == 0 || s.Synonyms["st"][0] != "street" {
			t.Errorf("expected NZ road synonyms dictionary to be configured")
		}
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestRunPipelineWithMockMeilisearch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/indexes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"taskUid":1,"indexUid":"addresses","status":"enqueued"}`))
	})
	mux.HandleFunc("/indexes/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"taskUid":1,"status":"enqueued"}`))
	})
	mux.HandleFunc("/tasks/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"uid":1,"status":"succeeded"}`))
	})

	httpClient := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			return rec.Result(), nil
		}),
	}

	cfg := &config.Config{
		MeiliHost:        "http://meili.local",
		MeiliAPIKey:      "test_key",
		BatchSize:        2,
		SuburbsCSVPath:   filepath.Join("..", "..", "testdata", "suburbs.csv"),
		RoadsCSVPath:     filepath.Join("..", "..", "testdata", "roads.csv"),
		AddressesCSVPath: filepath.Join("..", "..", "testdata", "addresses.csv"),
	}

	client := meilisearch.New(
		cfg.MeiliHost,
		meilisearch.WithAPIKey(cfg.MeiliAPIKey),
		meilisearch.WithCustomClient(httpClient),
	)
	indexer := NewIndexer(client, cfg.BatchSize)
	dl := NewDownloader(cfg.LINZBaseURL, cfg.LINZAPIKey)

	if err := indexer.RunPipeline(context.Background(), cfg, dl); err != nil {
		t.Fatalf("RunPipeline failed: %v", err)
	}
}

func TestDownloaderWFSURL(t *testing.T) {
	dl := NewDownloader("https://data.linz.govt.nz", "dummy_key")
	u := dl.buildWFSURL("data.linz.govt.nz:layer-113764", nil)
	if !strings.Contains(u, "typeNames=data.linz.govt.nz%3Alayer-113764") {
		t.Errorf("expected data.linz.govt.nz:layer-113764 in WFS URL, got %s", u)
	}
}

