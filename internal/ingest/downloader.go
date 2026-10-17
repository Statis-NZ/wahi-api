package ingest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	LayerSuburbs      = 113764 // NZ Suburbs and Localities
	LayerRoadSections = 123109 // NZ Addresses: Road Sections
	LayerAddresses    = 123113 // NZ Addresses
)

// Downloader streams CSV exports or changesets from the LINZ Data Service (LDS) WFS API,
// or opens local CSV files when paths are provided.
type Downloader struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// NewDownloader creates a Downloader configured for the LINZ Data Service.
func NewDownloader(baseURL, apiKey string) *Downloader {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://data.linz.govt.nz"
	}
	return &Downloader{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  strings.TrimSpace(apiKey),
		HTTPClient: &http.Client{
			Timeout: 30 * time.Minute,
		},
	}
}

// OpenLayerCSV returns a streaming io.ReadCloser for the given LINZ layer ID.
// If localPath is non-empty, the local CSV file is opened directly.
func (d *Downloader) OpenLayerCSV(ctx context.Context, layerID int, localPath string) (io.ReadCloser, error) {
	if strings.TrimSpace(localPath) != "" {
		f, err := os.Open(localPath)
		if err != nil {
			return nil, fmt.Errorf("open local CSV %q for layer %d: %w", localPath, layerID, err)
		}
		return f, nil
	}

	if d.APIKey == "" {
		return nil, fmt.Errorf("LINZ_API_KEY is required when no local CSV path is provided for layer %d", layerID)
	}

	reqURL := d.buildWFSURL(fmt.Sprintf("layer-%d", layerID), nil)
	return d.streamURL(ctx, reqURL)
}

// OpenChangesetCSV streams a CSV changeset for a LINZ layer between two ISO8601 timestamps.
func (d *Downloader) OpenChangesetCSV(ctx context.Context, layerID int, fromTime, toTime string) (io.ReadCloser, error) {
	if d.APIKey == "" {
		return nil, fmt.Errorf("LINZ_API_KEY is required to stream changesets for layer %d", layerID)
	}
	extra := url.Values{}
	extra.Set("viewparams", fmt.Sprintf("from:%s;to:%s", fromTime, toTime))
	reqURL := d.buildWFSURL(fmt.Sprintf("layer-%d-changeset", layerID), extra)
	return d.streamURL(ctx, reqURL)
}

func (d *Downloader) buildWFSURL(typeName string, extra url.Values) string {
	q := url.Values{}
	q.Set("service", "WFS")
	q.Set("version", "2.0.0")
	q.Set("request", "GetFeature")
	q.Set("typeNames", typeName)
	q.Set("outputFormat", "csv")
	for k, vals := range extra {
		for _, v := range vals {
			q.Add(k, v)
		}
	}
	return fmt.Sprintf("%s/services;key=%s/wfs?%s", d.BaseURL, url.PathEscape(d.APIKey), q.Encode())
}

func (d *Downloader) streamURL(ctx context.Context, reqURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create LINZ WFS request: %w", err)
	}
	req.Header.Set("User-Agent", "wahi-api/1.0")

	resp, err := d.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute LINZ WFS request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("LINZ WFS request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return resp.Body, nil
}
