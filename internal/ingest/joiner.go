package ingest

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/statis/wahi-api/internal/models"
	"github.com/twpayne/go-geom"
	"github.com/twpayne/go-geom/encoding/wkt"
)

// NormalizeSuburbKey normalizes a suburb/locality name for case-insensitive lookup.
func NormalizeSuburbKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// ParseWKTToGeo parses a WKT geometry string (Point, LineString, MultiLineString,
// Polygon, or MultiPolygon) in either EPSG:4167 (NZGD2000/WGS84 degrees) or
// EPSG:2193 (NZTM2000 meters) and returns a MeiliGeo {Lat, Lng}.
func ParseWKTToGeo(rawWKT string) (*models.MeiliGeo, error) {
	trimmed := strings.TrimSpace(rawWKT)
	if trimmed == "" {
		return nil, nil
	}

	// Strip optional EWKT prefix such as "SRID=2193;" or "SRID=4167;"
	if idx := strings.Index(trimmed, ";"); idx != -1 && strings.HasPrefix(strings.ToUpper(trimmed), "SRID=") {
		trimmed = strings.TrimSpace(trimmed[idx+1:])
	}

	g, err := wkt.Unmarshal(trimmed)
	if err != nil {
		return nil, fmt.Errorf("unmarshal WKT: %w", err)
	}

	var x, y float64
	switch v := g.(type) {
	case *geom.Point:
		coords := v.Coords()
		if len(coords) < 2 {
			return nil, fmt.Errorf("empty point geometry")
		}
		x, y = coords[0], coords[1]

	case *geom.LineString:
		num := v.NumCoords()
		if num == 0 {
			return nil, fmt.Errorf("empty linestring geometry")
		}
		mid := v.Coord(num / 2)
		x, y = mid[0], mid[1]

	case *geom.MultiLineString:
		if v.NumLineStrings() == 0 {
			return nil, fmt.Errorf("empty multilinestring geometry")
		}
		ls := v.LineString(v.NumLineStrings() / 2)
		if ls.NumCoords() == 0 {
			return nil, fmt.Errorf("empty linestring in multilinestring")
		}
		mid := ls.Coord(ls.NumCoords() / 2)
		x, y = mid[0], mid[1]

	case *geom.Polygon, *geom.MultiPolygon:
		x, y, err = extractCentroidOrBoundsCenter(g)
		if err != nil {
			return nil, err
		}

	default:
		x, y, err = extractCentroidOrBoundsCenter(g)
		if err != nil {
			return nil, err
		}
	}

	lat, lng := normalizeOrConvertCoords(x, y)
	return &models.MeiliGeo{
		Lat: math.Round(lat*1e6) / 1e6,
		Lng: math.Round(lng*1e6) / 1e6,
	}, nil
}

func extractCentroidOrBoundsCenter(g geom.T) (float64, float64, error) {
	flat := g.FlatCoords()
	stride := g.Stride()
	if len(flat) >= stride && stride >= 2 {
		var sumX, sumY float64
		count := len(flat) / stride
		// Exclude closing duplicate vertex on simple rings when count > 1
		if count > 1 && flat[0] == flat[(count-1)*stride] && flat[1] == flat[(count-1)*stride+1] {
			count--
		}
		for i := 0; i < count; i++ {
			sumX += flat[i*stride]
			sumY += flat[i*stride+1]
		}
		return sumX / float64(count), sumY / float64(count), nil
	}
	b := g.Bounds()
	if b.IsEmpty() {
		return 0, 0, fmt.Errorf("empty geometry bounds")
	}
	return (b.Min(0) + b.Max(0)) / 2.0, (b.Min(1) + b.Max(1)) / 2.0, nil
}

// normalizeOrConvertCoords detects whether (x, y) is in NZTM2000 (EPSG:2193 meters)
// or geographic degrees (EPSG:4167 / WGS84) and returns (lat, lng).
func normalizeOrConvertCoords(x, y float64) (lat, lng float64) {
	// Check if coordinates are in NZTM2000 meters (values > 100,000)
	if math.Abs(x) > 100000 && math.Abs(y) > 100000 {
		// In NZTM2000, Easting is ~1,000,000..2,600,000 and Northing is ~4,600,000..6,300,000
		easting, northing := x, y
		if x > 3500000 && y < 3500000 {
			// Swapped (Northing, Easting) axis order
			easting, northing = y, x
		}
		return nztm2000ToLatLon(easting, northing)
	}

	// Coordinates are in degrees (EPSG:4167 / EPSG:4326).
	// Standard WKT is POINT(lon lat), where NZ lat is in [-60, -25] and lon is in [160, 180] or [-180, -170].
	// However, some WFS exports emit (lat lon); detect by range.
	if x >= -60 && x <= -25 && (y >= 160 || y <= -170) {
		return x, y
	}
	return y, x
}

// nztm2000ToLatLon converts NZTM2000 (EPSG:2193) Easting and Northing (in meters)
// to NZGD2000 / WGS84 latitude and longitude (in decimal degrees).
// Reference: LINZ Standard for New Zealand Transverse Mercator 2000 (LINZS25002).
func nztm2000ToLatLon(easting, northing float64) (latDeg, lonDeg float64) {
	const (
		a   = 6378137.0             // GRS80 semi-major axis
		rf  = 298.257222101         // GRS80 inverse flattening
		k0  = 0.9996                // Central meridian scale factor
		fe  = 1600000.0             // False Easting (m)
		fn  = 10000000.0            // False Northing (m)
		cm0 = 173.0 * math.Pi / 180 // Central meridian (173° E in radians)
	)

	f := 1.0 / rf
	e2 := 2.0*f - f*f
	e4 := e2 * e2
	e6 := e4 * e2

	n1 := northing - fn
	m1 := n1 / k0

	a0 := 1.0 - e2/4.0 - 3.0*e4/64.0 - 5.0*e6/256.0
	mu := m1 / (a * a0)

	e1 := (1.0 - math.Sqrt(1.0-e2)) / (1.0 + math.Sqrt(1.0-e2))
	e12 := e1 * e1
	e13 := e12 * e1
	e14 := e13 * e1

	j1 := 3.0*e1/2.0 - 27.0*e13/32.0
	j2 := 21.0*e12/16.0 - 55.0*e14/32.0
	j3 := 151.0 * e13 / 96.0
	j4 := 1097.0 * e14 / 512.0

	fp := mu + j1*math.Sin(2.0*mu) + j2*math.Sin(4.0*mu) + j3*math.Sin(6.0*mu) + j4*math.Sin(8.0*mu)

	sinFp := math.Sin(fp)
	cosFp := math.Cos(fp)
	tanFp := sinFp / cosFp
	tan2 := tanFp * tanFp
	tan4 := tan2 * tan2

	ep2 := e2 / (1.0 - e2)
	c1 := ep2 * cosFp * cosFp
	c12 := c1 * c1

	denom := math.Sqrt(1.0 - e2*sinFp*sinFp)
	v1 := a / denom
	p1 := a * (1.0 - e2) / (denom * denom * denom)

	d := (easting - fe) / (v1 * k0)
	d2 := d * d
	d3 := d2 * d
	d4 := d3 * d
	d5 := d4 * d
	d6 := d5 * d

	latRad := fp - (v1*tanFp/p1)*(d2/2.0-(5.0+3.0*tan2+10.0*c1-4.0*c12-9.0*ep2)*d4/24.0+(61.0+90.0*tan2+298.0*c1+45.0*tan4-252.0*ep2-3.0*c12)*d6/720.0)
	lonRad := cm0 + (d-(1.0+2.0*tan2+c1)*d3/6.0+(5.0-2.0*c1+28.0*tan2-3.0*c12+8.0*ep2+24.0*tan4)*d5/120.0)/cosFp

	return latRad * 180.0 / math.Pi, lonRad * 180.0 / math.Pi
}

// LoadSuburbsCSV parses the LINZ NZ Suburbs and Localities CSV (Layer 113764),
// returning the documents for the "localities" index and an in-memory lookup map
// keyed by normalized lowercase suburb/locality name.
func LoadSuburbsCSV(r io.Reader) ([]models.LocalityDocument, map[string]models.SuburbMeta, error) {
	cr := csv.NewReader(r)
	cr.ReuseRecord = true
	cr.LazyQuotes = true

	header, err := cr.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("read suburbs CSV header: %w", err)
	}
	colIdx := buildColumnMap(header)

	var docs []models.LocalityDocument
	lookup := make(map[string]models.SuburbMeta, 3200)

	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read suburbs CSV row: %w", err)
		}

		id, err := strconv.Atoi(getCol(rec, colIdx, "id"))
		if err != nil {
			continue
		}
		name := getCol(rec, colIdx, "name")
		if name == "" {
			continue
		}
		additionalName := getCol(rec, colIdx, "additional_name")
		locType := getCol(rec, colIdx, "type")
		majorName := getCol(rec, colIdx, "major_name")
		majorNameType := getCol(rec, colIdx, "major_name_type")
		ta := getCol(rec, colIdx, "territorial_authority")
		pop, _ := strconv.Atoi(getCol(rec, colIdx, "population_estimate"))
		shapeStr := getShapeCol(rec, colIdx)

		geo, _ := ParseWKTToGeo(shapeStr)

		docs = append(docs, models.LocalityDocument{
			ID:                   id,
			Name:                 name,
			AdditionalName:       additionalName,
			Type:                 locType,
			MajorName:            majorName,
			MajorNameType:        majorNameType,
			TerritorialAuthority: ta,
			PopulationEstimate:   pop,
			Geo:                  geo,
		})

		key := NormalizeSuburbKey(name)
		if existing, exists := lookup[key]; !exists || pop > existing.PopulationEstimate {
			lookup[key] = models.SuburbMeta{
				PopulationEstimate: pop,
				AdditionalName:     additionalName,
			}
		}
	}

	return docs, lookup, nil
}

// LoadRoadSectionsCSV parses the LINZ NZ Addresses: Road Sections CSV (Layer 123109),
// aggregates unique streets by road_id + suburb_locality + town_city, enriches them
// with suburb metadata, and returns deduplicated StreetDocuments plus a RoadAliases lookup map.
func LoadRoadSectionsCSV(r io.Reader, suburbs map[string]models.SuburbMeta) ([]models.StreetDocument, map[int]models.RoadAliases, error) {
	cr := csv.NewReader(r)
	cr.ReuseRecord = true
	cr.LazyQuotes = true

	header, err := cr.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("read road sections CSV header: %w", err)
	}
	colIdx := buildColumnMap(header)

	var streets []models.StreetDocument
	seenStreets := make(map[string]struct{}, 64000)
	roadAliases := make(map[int]models.RoadAliases, 64000)

	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read road sections CSV row: %w", err)
		}

		roadID, err := strconv.Atoi(getCol(rec, colIdx, "road_id"))
		if err != nil {
			continue
		}
		roadSectionID, _ := strconv.Atoi(getCol(rec, colIdx, "road_section_id"))
		fullRoadName := getCol(rec, colIdx, "full_road_name")
		if fullRoadName == "" {
			continue
		}
		roadNameLabel := getCol(rec, colIdx, "road_name_label")
		secondaryRoadName := getCol(rec, colIdx, "secondary_road_name")
		tertiaryRoadName := getCol(rec, colIdx, "tertiary_road_name")
		suburbLocality := getCol(rec, colIdx, "suburb_locality")
		townCity := getCol(rec, colIdx, "town_city")
		ta := getCol(rec, colIdx, "territorial_authority")
		isLand := parseBool(getCol(rec, colIdx, "is_land"), true)
		shapeStr := getShapeCol(rec, colIdx)

		// Maintain compact road_id -> RoadAliases map
		existingAlias := roadAliases[roadID]
		if existingAlias.RoadNameLabel == "" && roadNameLabel != "" {
			existingAlias.RoadNameLabel = roadNameLabel
		}
		if existingAlias.SecondaryRoadName == "" && secondaryRoadName != "" {
			existingAlias.SecondaryRoadName = secondaryRoadName
		}
		if existingAlias.TertiaryRoadName == "" && tertiaryRoadName != "" {
			existingAlias.TertiaryRoadName = tertiaryRoadName
		}
		roadAliases[roadID] = existingAlias

		dedupKey := fmt.Sprintf("%d|%s|%s", roadID, strings.ToLower(suburbLocality), strings.ToLower(townCity))
		if _, exists := seenStreets[dedupKey]; exists {
			continue
		}
		seenStreets[dedupKey] = struct{}{}

		meta := suburbs[NormalizeSuburbKey(suburbLocality)]
		geo, _ := ParseWKTToGeo(shapeStr)

		docID := fmt.Sprintf("%d_%d", roadID, roadSectionID)
		if roadSectionID == 0 {
			docID = fmt.Sprintf("%d_%d", roadID, len(streets)+1)
		}

		streets = append(streets, models.StreetDocument{
			ID:                   docID,
			RoadSectionID:        roadSectionID,
			RoadID:               roadID,
			FullRoadName:         fullRoadName,
			RoadNameLabel:        roadNameLabel,
			SecondaryRoadName:    secondaryRoadName,
			TertiaryRoadName:     tertiaryRoadName,
			SuburbLocality:       suburbLocality,
			SuburbAliases:        meta.AdditionalName,
			TownCity:             townCity,
			TerritorialAuthority: ta,
			IsLand:               isLand,
			PopulationEstimate:   meta.PopulationEstimate,
			Geo:                  geo,
		})
	}

	return streets, roadAliases, nil
}

// StreamAddressesCSV streams the LINZ NZ Addresses CSV (Layer 123113) in batches of batchSize,
// enriching each row with road aliases and suburb metadata without loading the full dataset into memory.
func StreamAddressesCSV(
	r io.Reader,
	suburbs map[string]models.SuburbMeta,
	roads map[int]models.RoadAliases,
	batchSize int,
	onBatch func([]models.AddressDocument) error,
) (int, error) {
	if batchSize <= 0 {
		batchSize = 50000
	}

	cr := csv.NewReader(r)
	cr.ReuseRecord = true
	cr.LazyQuotes = true

	header, err := cr.Read()
	if err != nil {
		return 0, fmt.Errorf("read addresses CSV header: %w", err)
	}
	colIdx := buildColumnMap(header)

	batch := make([]models.AddressDocument, 0, batchSize)
	total := 0

	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return total, fmt.Errorf("read addresses CSV row: %w", err)
		}

		addressID, err := strconv.Atoi(getCol(rec, colIdx, "address_id"))
		if err != nil {
			continue
		}
		roadID, _ := strconv.Atoi(getCol(rec, colIdx, "road_id"))
		fullAddrNum := getCol(rec, colIdx, "full_address_number")
		fullRoadName := getCol(rec, colIdx, "full_road_name")
		fullAddr := getCol(rec, colIdx, "full_address")
		ta := getCol(rec, colIdx, "territorial_authority")
		unit := getCol(rec, colIdx, "unit")
		addrNum, _ := strconv.Atoi(getCol(rec, colIdx, "address_number"))
		addrNumSuffix := getCol(rec, colIdx, "address_number_suffix")

		var addrNumHigh *int
		if highStr := getCol(rec, colIdx, "address_number_high"); highStr != "" {
			if highVal, err := strconv.Atoi(highStr); err == nil {
				addrNumHigh = &highVal
			}
		}

		roadName := getCol(rec, colIdx, "road_name")
		roadNameType := getCol(rec, colIdx, "road_name_type")
		roadNameSuffix := getCol(rec, colIdx, "road_name_suffix")
		suburbLocality := getCol(rec, colIdx, "suburb_locality")
		townCity := getCol(rec, colIdx, "town_city")
		isLand := parseBool(getCol(rec, colIdx, "is_land"), true)
		lifecycle := getCol(rec, colIdx, "address_lifecycle")
		if lifecycle == "" {
			lifecycle = "Current"
		}
		shapeStr := getShapeCol(rec, colIdx)
		geo, _ := ParseWKTToGeo(shapeStr)

		roadMeta := roads[roadID]
		suburbMeta := suburbs[NormalizeSuburbKey(suburbLocality)]

		batch = append(batch, models.AddressDocument{
			AddressID:            addressID,
			RoadID:               roadID,
			FullAddressNumber:    fullAddrNum,
			FullRoadName:         fullRoadName,
			FullAddress:          fullAddr,
			TerritorialAuthority: ta,
			Unit:                 unit,
			AddressNumber:        addrNum,
			AddressNumberSuffix:  addrNumSuffix,
			AddressNumberHigh:    addrNumHigh,
			RoadName:             roadName,
			RoadNameType:         roadNameType,
			RoadNameSuffix:       roadNameSuffix,
			RoadNameLabel:        roadMeta.RoadNameLabel,
			SecondaryRoadName:    roadMeta.SecondaryRoadName,
			SuburbLocality:       suburbLocality,
			SuburbAliases:        suburbMeta.AdditionalName,
			TownCity:             townCity,
			IsLand:               isLand,
			AddressLifecycle:     lifecycle,
			PopulationEstimate:   suburbMeta.PopulationEstimate,
			Geo:                  geo,
		})
		total++

		if len(batch) >= batchSize {
			if err := onBatch(batch); err != nil {
				return total, err
			}
			batch = batch[:0]
		}
	}

	if len(batch) > 0 {
		if err := onBatch(batch); err != nil {
			return total, err
		}
	}

	return total, nil
}

func buildColumnMap(header []string) map[string]int {
	m := make(map[string]int, len(header))
	for i, h := range header {
		clean := strings.TrimPrefix(strings.TrimSpace(h), "\ufeff")
		m[strings.ToLower(clean)] = i
	}
	return m
}

func getCol(rec []string, colIdx map[string]int, name string) string {
	idx, ok := colIdx[name]
	if !ok || idx < 0 || idx >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[idx])
}

func getShapeCol(rec []string, colIdx map[string]int) string {
	for _, candidate := range []string{"shape", "wkt", "geom", "geometry", "the_geom"} {
		if v := getCol(rec, colIdx, candidate); v != "" {
			return v
		}
	}
	return ""
}

func parseBool(s string, defaultVal bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "t", "1", "yes", "y":
		return true
	case "false", "f", "0", "no", "n":
		return false
	default:
		return defaultVal
	}
}
