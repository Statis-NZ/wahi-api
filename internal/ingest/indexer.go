package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"runtime/debug"
	"time"

	meilisearch "github.com/meilisearch/meilisearch-go"
	"github.com/statis/wahi-api/internal/config"
	"github.com/statis/wahi-api/internal/models"
)

const (
	IndexAddresses  = "addresses"
	IndexStreets    = "streets"
	IndexLocalities = "localities"
)

// DefaultSynonyms returns the NZ road abbreviation synonyms dictionary.
// Abbreviations expand one-way to full words (e.g. "st" -> "street") because LINZ
// full_road_name always spells out the road type while road_name_label stores the abbreviation.
// Avoiding reverse full-word -> 2-letter abbreviation expansion prevents Meilisearch from
// treating "street" as a 2-letter prefix wildcard ("st*").
func DefaultSynonyms() map[string][]string {
	return map[string][]string{
		"st":   {"street"},
		"rd":   {"road"},
		"ave":  {"avenue"},
		"av":   {"avenue"},
		"dr":   {"drive"},
		"pl":   {"place"},
		"cres": {"crescent"},
		"cr":   {"crescent"},
		"tce":  {"terrace"},
		"ter":  {"terrace"},
		"hwy":  {"highway"},
		"sh":   {"state highway"},
		"ln":   {"lane"},
		"ct":   {"court"},
		"crt":  {"court"},
		"qy":   {"quay"},
		"esp":  {"esplanade"},
		"pde":  {"parade"},
		"blvd": {"boulevard"},
		"cl":   {"close"},
		"grv":  {"grove"},
	}
}

// LocalitySynonyms returns synonyms specific to NZ suburbs and localities (without road-type suffixes).
func LocalitySynonyms() map[string][]string {
	return map[string][]string{
		"mt":    {"mount"},
		"mount": {"mt"},
		"pt":    {"point"},
		"point": {"pt"},
		"st":    {"saint"},
		"saint": {"st"},
		"cbd":   {"central"},
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
			"address_number_suffix",
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
		Synonyms:     LocalitySynonyms(),
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
		if createTask, err := idx.Client.CreateIndexWithContext(ctx, &meilisearch.IndexConfig{
			Uid:        c.uid,
			PrimaryKey: c.primaryKey,
		}); err == nil && createTask != nil {
			_, _ = idx.Client.WaitForTaskWithContext(ctx, createTask.TaskUID, 200*time.Millisecond)
		}
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

// ConfigureIndexesWithRetry repeatedly attempts to configure Meilisearch indexes using
// exponential backoff capped at maxBackoff until it succeeds or ctx is cancelled.
func (idx *Indexer) ConfigureIndexesWithRetry(ctx context.Context, initialBackoff, maxBackoff time.Duration) error {
	if initialBackoff <= 0 {
		initialBackoff = time.Second
	}
	if maxBackoff < initialBackoff {
		maxBackoff = 30 * time.Second
	}
	backoff := initialBackoff

	for attempt := 1; ; attempt++ {
		err := idx.ConfigureIndexes(ctx)
		if err == nil {
			if attempt > 1 {
				slog.Info("synchronized Meilisearch index settings after retry", "attempt", attempt)
			}
			return nil
		}
		if isPermanentMeiliError(err) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("failed to synchronize Meilisearch index settings; retrying",
			"attempt", attempt,
			"retry_in", backoff,
			"error", err,
		)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func isPermanentMeiliError(err error) bool {
	var meiliErr *meilisearch.Error
	if errors.As(err, &meiliErr) {
		return meiliErr.StatusCode >= http.StatusBadRequest &&
			meiliErr.StatusCode < http.StatusInternalServerError &&
			meiliErr.StatusCode != http.StatusNotFound &&
			meiliErr.StatusCode != http.StatusRequestTimeout &&
			meiliErr.StatusCode != http.StatusTooManyRequests
	}
	return false
}

// IndexLocalities batch-inserts LocalityDocuments into the "localities" index using NDJSON.
func (idx *Indexer) IndexLocalities(ctx context.Context, docs []models.LocalityDocument) error {
	if len(docs) == 0 {
		return nil
	}
	ndjsonBytes, err := encodeNDJSON(docs)
	if err != nil {
		return fmt.Errorf("encode localities NDJSON: %w", err)
	}
	pk := "id"
	_, err = idx.Client.Index(IndexLocalities).AddDocumentsNdjsonWithContext(
		ctx,
		ndjsonBytes,
		&meilisearch.DocumentOptions{PrimaryKey: &pk},
	)
	if err != nil {
		return fmt.Errorf("index localities: %w", err)
	}
	return nil
}

// IndexStreets batch-inserts StreetDocuments into the "streets" index using NDJSON.
func (idx *Indexer) IndexStreets(ctx context.Context, docs []models.StreetDocument) error {
	if len(docs) == 0 {
		return nil
	}
	ndjsonBytes, err := encodeNDJSON(docs)
	if err != nil {
		return fmt.Errorf("encode streets NDJSON: %w", err)
	}
	pk := "id"
	_, err = idx.Client.Index(IndexStreets).AddDocumentsNdjsonWithContext(
		ctx,
		ndjsonBytes,
		&meilisearch.DocumentOptions{PrimaryKey: &pk},
	)
	if err != nil {
		return fmt.Errorf("index streets: %w", err)
	}
	return nil
}

// IndexAddressBatch sends a batch of AddressDocuments to the "addresses" index using NDJSON,
// chunking large batches into 10,000-document payloads to avoid large contiguous buffer allocations.
func (idx *Indexer) IndexAddressBatch(ctx context.Context, batch []models.AddressDocument) error {
	if len(batch) == 0 {
		return nil
	}
	const maxChunk = 10000
	pk := "address_id"
	opts := &meilisearch.DocumentOptions{PrimaryKey: &pk}

	for start := 0; start < len(batch); start += maxChunk {
		end := start + maxChunk
		if end > len(batch) {
			end = len(batch)
		}
		ndjsonBytes, err := encodeNDJSON(batch[start:end])
		if err != nil {
			return fmt.Errorf("encode addresses NDJSON: %w", err)
		}
		if _, err := idx.Client.Index(IndexAddresses).AddDocumentsNdjsonWithContext(ctx, ndjsonBytes, opts); err != nil {
			return fmt.Errorf("index addresses batch: %w", err)
		}
	}
	return nil
}

func encodeNDJSON[T any](items []T) ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(len(items) * 256)
	enc := json.NewEncoder(&buf)
	for i := range items {
		if err := enc.Encode(&items[i]); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// RunPipeline executes the 3-phase LINZ ETL pipeline:
// 1. Configure Meilisearch indexes & synonyms
// 2. Load NZ Suburbs and Localities into memory & index into "localities"
// 3. Stream NZ Road Sections, enrich with suburb populations/aliases & index into "streets"
// 4. Stream NZ Addresses in batches of 50,000, enrich from in-memory maps & index into "addresses"
// 5. Free in-memory maps once ingestion finishes.
func (idx *Indexer) RunPipeline(ctx context.Context, cfg *config.Config, dl *Downloader) error {
	// Set a soft Go memory limit of 180 MiB so the garbage collector stays well below
	// the 256MB Docker container memory limit during streaming ETL.
	debug.SetMemoryLimit(180 * 1024 * 1024)

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
	runtime.GC()

	// Step 3.2: Stream NZ Addresses: Road Sections (Layer 123109)
	slog.Info("loading NZ Addresses: Road Sections", "layer", LayerRoadSections, "local_path", cfg.RoadsCSVPath)
	roadsRC, err := dl.OpenLayerCSV(ctx, LayerRoadSections, cfg.RoadsCSVPath)
	if err != nil {
		return err
	}
	totalStreets := 0
	roadMap, err := StreamRoadSectionsCSV(roadsRC, suburbMap, 10000, func(batch []models.StreetDocument) error {
		totalStreets += len(batch)
		slog.Info("indexing deduplicated streets batch into Meilisearch", "batch_size", len(batch), "total_streets", totalStreets)
		return idx.IndexStreets(ctx, batch)
	})
	_ = roadsRC.Close()
	if err != nil {
		return err
	}
	slog.Info("completed indexing streets", "total_unique_streets", totalStreets, "road_ids", len(roadMap))
	runtime.GC()

	// Step 3.3: Stream NZ Addresses (Layer 123113)
	slog.Info("streaming NZ Addresses", "layer", LayerAddresses, "local_path", cfg.AddressesCSVPath, "batch_size", idx.BatchSize)
	addrRC, err := dl.OpenLayerCSV(ctx, LayerAddresses, cfg.AddressesCSVPath)
	if err != nil {
		return err
	}
	defer func() { _ = addrRC.Close() }()

	batchNum := 0
	totalAddresses, err := StreamAddressesCSV(addrRC, suburbMap, roadMap, idx.BatchSize, func(batch []models.AddressDocument) error {
		batchNum++
		slog.Info("sending address batch to Meilisearch", "batch", batchNum, "size", len(batch), "total_sent", batchNum*len(batch))
		return idx.IndexAddressBatch(ctx, batch)
	})
	if err != nil {
		return err
	}

	runtime.GC()
	debug.FreeOSMemory()

	slog.Info("ingestion pipeline completed", "total_addresses", totalAddresses)
	return nil
}
