package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"github.com/statis/wahi-api/internal/config"
	"github.com/statis/wahi-api/internal/models"
)

const (
	IndexAddresses  = "addresses"
	IndexStreets    = "streets"
	IndexLocalities = "localities"
)

// DefaultSynonyms returns the NZ road and locality synonyms dictionary from SPEC.md Section 4.1.
func DefaultSynonyms() map[string][]string {
	return map[string][]string{
		"st":       {"street"},
		"street":   {"st"},
		"rd":       {"road"},
		"road":     {"rd"},
		"ave":      {"avenue"},
		"avenue":   {"ave"},
		"dr":       {"drive"},
		"drive":    {"dr"},
		"pl":       {"place"},
		"place":    {"pl"},
		"cres":     {"crescent"},
		"crescent": {"cres"},
		"tce":      {"terrace"},
		"terrace":  {"tce"},
		"hwy":      {"highway"},
		"sh":       {"state highway"},
		"ln":       {"lane"},
		"ct":       {"court"},
		"mt":       {"mount"},
		"pt":       {"point"},
		"cbd":      {"central"},
	}
}

// DefaultRankingRules returns the Meilisearch ranking rules with population_estimate:desc tie-breaking.
func DefaultRankingRules() []string {
	return []string{
		"words",
		"typo",
		"proximity",
		"attributeRank",
		"wordPosition",
		"sort",
		"exactness",
		"population_estimate:desc",
	}
}

// AddressesIndexSettings returns the strict Meilisearch settings for the "addresses" index.
func AddressesIndexSettings() *meilisearch.Settings {
	return &meilisearch.Settings{
		SearchableAttributes: []string{
			"full_address_number",
			"full_road_name",
			"road_name_label",
			"secondary_road_name",
			"suburb_locality",
			"suburb_aliases",
			"town_city",
		},
		FilterableAttributes: []string{
			"address_number",
			"address_number_high",
			"unit",
			"suburb_locality",
			"town_city",
			"territorial_authority",
			"is_land",
			"address_lifecycle",
			"_geo",
		},
		SortableAttributes: []string{
			"population_estimate",
			"_geo",
		},
		RankingRules: DefaultRankingRules(),
		Synonyms:     DefaultSynonyms(),
	}
}

// StreetsIndexSettings returns the strict Meilisearch settings for the "streets" index.
func StreetsIndexSettings() *meilisearch.Settings {
	return &meilisearch.Settings{
		SearchableAttributes: []string{
			"full_road_name",
			"road_name_label",
			"secondary_road_name",
			"tertiary_road_name",
			"suburb_locality",
			"suburb_aliases",
			"town_city",
		},
		FilterableAttributes: []string{
			"suburb_locality",
			"town_city",
			"territorial_authority",
			"is_land",
			"_geo",
		},
		SortableAttributes: []string{
			"population_estimate",
			"_geo",
		},
		RankingRules: DefaultRankingRules(),
		Synonyms:     DefaultSynonyms(),
	}
}

// LocalitiesIndexSettings returns the strict Meilisearch settings for the "localities" index.
func LocalitiesIndexSettings() *meilisearch.Settings {
	return &meilisearch.Settings{
		SearchableAttributes: []string{
			"name",
			"additional_name",
			"major_name",
			"territorial_authority",
		},
		FilterableAttributes: []string{
			"type",
			"major_name",
			"territorial_authority",
			"_geo",
		},
		SortableAttributes: []string{
			"population_estimate",
			"_geo",
		},
		RankingRules: DefaultRankingRules(),
		Synonyms:     DefaultSynonyms(),
	}
}

// Indexer manages Meilisearch index configuration and batched document ingestion.
type Indexer struct {
	Client    meilisearch.ServiceManager
	BatchSize int
}

// NewIndexer creates a new Indexer backed by the provided Meilisearch ServiceManager.
func NewIndexer(client meilisearch.ServiceManager, batchSize int) *Indexer {
	if batchSize <= 0 {
		batchSize = 50000
	}
	return &Indexer{
		Client:    client,
		BatchSize: batchSize,
	}
}

// ConfigureIndexes creates and configures the localities, streets, and addresses indexes.
func (idx *Indexer) ConfigureIndexes(ctx context.Context) error {
	configs := []struct {
		uid        string
		primaryKey string
		settings   *meilisearch.Settings
	}{
		{IndexLocalities, "id", LocalitiesIndexSettings()},
		{IndexStreets, "id", StreetsIndexSettings()},
		{IndexAddresses, "address_id", AddressesIndexSettings()},
	}

	for _, c := range configs {
		_, _ = idx.Client.CreateIndexWithContext(ctx, &meilisearch.IndexConfig{
			Uid:        c.uid,
			PrimaryKey: c.primaryKey,
		})
		index := idx.Client.Index(c.uid)
		task, err := index.UpdateSettingsWithContext(ctx, c.settings)
		if err != nil {
			return fmt.Errorf("update settings for index %q: %w", c.uid, err)
		}
		if _, err := idx.Client.WaitForTaskWithContext(ctx, task.TaskUID, 200*time.Millisecond); err != nil {
			return fmt.Errorf("wait for settings task on index %q: %w", c.uid, err)
		}
	}

	return nil
}

// IndexLocalities batch-inserts LocalityDocuments into the "localities" index.
func (idx *Indexer) IndexLocalities(ctx context.Context, docs []models.LocalityDocument) error {
	if len(docs) == 0 {
		return nil
	}
	pk := "id"
	_, err := idx.Client.Index(IndexLocalities).AddDocumentsInBatchesWithContext(
		ctx,
		docs,
		idx.BatchSize,
		&meilisearch.DocumentOptions{PrimaryKey: &pk},
	)
	if err != nil {
		return fmt.Errorf("index localities: %w", err)
	}
	return nil
}

// IndexStreets batch-inserts StreetDocuments into the "streets" index.
func (idx *Indexer) IndexStreets(ctx context.Context, docs []models.StreetDocument) error {
	if len(docs) == 0 {
		return nil
	}
	pk := "id"
	_, err := idx.Client.Index(IndexStreets).AddDocumentsInBatchesWithContext(
		ctx,
		docs,
		idx.BatchSize,
		&meilisearch.DocumentOptions{PrimaryKey: &pk},
	)
	if err != nil {
		return fmt.Errorf("index streets: %w", err)
	}
	return nil
}

// IndexAddressBatch sends a batch of AddressDocuments to the "addresses" index using NDJSON.
func (idx *Indexer) IndexAddressBatch(ctx context.Context, batch []models.AddressDocument) error {
	if len(batch) == 0 {
		return nil
	}
	ndjsonBytes, err := encodeNDJSON(batch)
	if err != nil {
		return fmt.Errorf("encode addresses NDJSON: %w", err)
	}
	pk := "address_id"
	_, err = idx.Client.Index(IndexAddresses).AddDocumentsNdjsonWithContext(
		ctx,
		ndjsonBytes,
		&meilisearch.DocumentOptions{PrimaryKey: &pk},
	)
	if err != nil {
		return fmt.Errorf("index addresses batch: %w", err)
	}
	return nil
}

func encodeNDJSON[T any](items []T) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for i := range items {
		if err := enc.Encode(items[i]); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// RunPipeline executes the 3-phase LINZ ETL pipeline:
// 1. Configure Meilisearch indexes & synonyms
// 2. Load NZ Suburbs and Localities into memory & index into "localities"
// 3. Load NZ Road Sections into memory, enrich with suburb populations/aliases & index into "streets"
// 4. Stream NZ Addresses in batches of 50,000, enrich from in-memory maps & index into "addresses"
// 5. Free in-memory maps once ingestion finishes.
func (idx *Indexer) RunPipeline(ctx context.Context, cfg *config.Config, dl *Downloader) error {
	slog.Info("configuring Meilisearch indexes and synonyms")
	if err := idx.ConfigureIndexes(ctx); err != nil {
		return err
	}

	// Step 3.1: Load NZ Suburbs and Localities (Layer 113764)
	slog.Info("loading NZ Suburbs and Localities", "layer", LayerSuburbs, "local_path", cfg.SuburbsCSVPath)
	suburbsRC, err := dl.OpenLayerCSV(ctx, LayerSuburbs, cfg.SuburbsCSVPath)
	if err != nil {
		return err
	}
	localityDocs, suburbMap, err := LoadSuburbsCSV(suburbsRC)
	_ = suburbsRC.Close()
	if err != nil {
		return err
	}
	slog.Info("indexing localities into Meilisearch", "count", len(localityDocs))
	if err := idx.IndexLocalities(ctx, localityDocs); err != nil {
		return err
	}
	localityDocs = nil // Free slice early

	// Step 3.2: Load NZ Addresses: Road Sections (Layer 123109)
	slog.Info("loading NZ Addresses: Road Sections", "layer", LayerRoadSections, "local_path", cfg.RoadsCSVPath)
	roadsRC, err := dl.OpenLayerCSV(ctx, LayerRoadSections, cfg.RoadsCSVPath)
	if err != nil {
		return err
	}
	streetDocs, roadMap, err := LoadRoadSectionsCSV(roadsRC, suburbMap)
	_ = roadsRC.Close()
	if err != nil {
		return err
	}
	slog.Info("indexing deduplicated streets into Meilisearch", "count", len(streetDocs))
	if err := idx.IndexStreets(ctx, streetDocs); err != nil {
		return err
	}
	streetDocs = nil // Free slice early

	// Step 3.3: Stream NZ Addresses (Layer 123113)
	slog.Info("streaming NZ Addresses", "layer", LayerAddresses, "local_path", cfg.AddressesCSVPath, "batch_size", idx.BatchSize)
	addrRC, err := dl.OpenLayerCSV(ctx, LayerAddresses, cfg.AddressesCSVPath)
	if err != nil {
		return err
	}
	defer addrRC.Close()

	batchNum := 0
	totalAddresses, err := StreamAddressesCSV(addrRC, suburbMap, roadMap, idx.BatchSize, func(batch []models.AddressDocument) error {
		batchNum++
		slog.Info("sending address batch to Meilisearch", "batch", batchNum, "size", len(batch))
		return idx.IndexAddressBatch(ctx, batch)
	})
	if err != nil {
		return err
	}

	// Free in-memory lookup maps once ingestion finishes
	suburbMap = nil
	roadMap = nil
	runtime.GC()

	slog.Info("ingestion pipeline completed", "total_addresses", totalAddresses)
	return nil
}
