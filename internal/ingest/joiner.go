package ingest

import (
	"encoding/csv"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/statis/wahi-api/internal/models"
	geom "github.com/twpayne/go-geom"
	"github.com/twpayne/go-geom/encoding/wkt"
)

// NormalizeSuburbKey normalizes a suburb/locality name for case-insensitive lookup.
func NormalizeSuburbKey(name string) string {
	return strings.Clone(strings.ToLower(strings.TrimSpace(name)))
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

	// Fast zero-allocation scanner for large geometries (> 4KB, such as complex coastline
	// MULTIPOLYGONs in NZ Suburbs and Localities) to avoid large AST allocations in memory-constrained containers.
	if len(trimmed) > 4096 {
		x, y, ok := scanWKTBoundsCenter(trimmed)
		if !ok {
			return nil, fmt.Errorf("failed to scan coordinates from large WKT")
		}
		lat, lng := normalizeOrConvertCoords(x, y)
		return &models.MeiliGeo{
			Lat: math.Round(lat*1e6) / 1e6,
			Lng: math.Round(lng*1e6) / 1e6,
		}, nil
	}

	g, err := wkt.Unmarshal(trimmed)
	if err != nil {
		// Fallback to streaming coordinate scanner if WKT has minor dialect differences
		if x, y, ok := scanWKTBoundsCenter(trimmed); ok {
			lat, lng := normalizeOrConvertCoords(x, y)
			return &models.MeiliGeo{
				Lat: math.Round(lat*1e6) / 1e6,
				Lng: math.Round(lng*1e6) / 1e6,
			}, nil
		}
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

// scanWKTBoundsCenter scans (x, y) coordinate pairs from a WKT string in a single pass
// with zero heap allocations, returning the bounding box center.
func scanWKTBoundsCenter(s string) (centerX, centerY float64, ok bool) {
	idx := strings.IndexByte(s, '(')
	if idx == -1 {
		return 0, 0, false
	}
	n := len(s)
	i := idx + 1

	minX, maxX := math.MaxFloat64, -math.MaxFloat64
	minY, maxY := math.MaxFloat64, -math.MaxFloat64
	count := 0

	for i < n {
		// Advance to start of first number in coordinate tuple
		for i < n && !isNumChar(s[i]) {
			i++
		}
		if i >= n {
			break
		}
		startX := i
		for i < n && isNumChar(s[i]) {
			i++
		}
		x, err1 := strconv.ParseFloat(s[startX:i], 64)

		// Skip whitespace between X and Y
		for i < n && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= n || !isNumChar(s[i]) {
			continue
		}
		startY := i
		for i < n && isNumChar(s[i]) {
			i++
		}
		y, err2 := strconv.ParseFloat(s[startY:i], 64)

		// Skip any optional Z/M coordinates up to next delimiter (',' or ')')
		for i < n && s[i] != ',' && s[i] != ')' && s[i] != '(' {
			i++
		}

		if err1 == nil && err2 == nil {
			if x < minX {
				minX = x
			}
			if x > maxX {
				maxX = x
			}
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
			count++
		}
	}

	if count == 0 {
		return 0, 0, false
	}
	return (minX + maxX) / 2.0, (minY + maxY) / 2.0, true
}

func isNumChar(b byte) bool {
	return (b >= '0' && b <= '9') || b == '-' || b == '+' || b == '.' || b == 'e' || b == 'E'
}

func extractCentroidOrBoundsCenter(g geom.T) (float64, float64, error) {
	flat := g.FlatCoords()
	stride := g.Stride()
	if len(flat) >= stride && stride >= 2 {
		var sumX, sumY float64
		count := len(flat) / stride
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
	if math.Abs(x) > 100000 && math.Abs(y) > 100000 {
		easting, northing := x, y
		if x > 3500000 && y < 3500000 {
			easting, northing = y, x
		}
		return nztm2000ToLatLon(easting, northing)
	}

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
		a   = 6378137.0
		rf  = 298.257222101
		k0  = 0.9996
		fe  = 1600000.0
		fn  = 10000000.0
		cm0 = 173.0 * math.Pi / 180
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

	docs := make([]models.LocalityDocument, 0, 3200)
	lookup := make(map[string]models.SuburbMeta, 3200)

	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read suburbs CSV row: %w", err)
		}

		id, err := strconv.Atoi(getColRaw(rec, colIdx, "id"))
		if err != nil {
			continue
		}
		name := getColCloned(rec, colIdx, "name")
		if name == "" {
			continue
		}
		additionalName := getColCloned(rec, colIdx, "additional_name")
		locType := getColCloned(rec, colIdx, "type")
		majorName := getColCloned(rec, colIdx, "major_name")
		majorNameType := getColCloned(rec, colIdx, "major_name_type")
		ta := getColCloned(rec, colIdx, "territorial_authority")
		pop, _ := strconv.Atoi(getColRaw(rec, colIdx, "population_estimate"))
		shapeStr := getShapeColRaw(rec, colIdx)

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
	var allStreets []models.StreetDocument
	roadAliases, err := StreamRoadSectionsCSV(r, suburbs, 10000, func(batch []models.StreetDocument) error {
		allStreets = append(allStreets, batch...)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return allStreets, roadAliases, nil
}

// StreamRoadSectionsCSV streams the Road Sections CSV, emitting deduplicated StreetDocuments
// in batches and returning a compact in-memory RoadAliases map keyed by road_id.
func StreamRoadSectionsCSV(
	r io.Reader,
	suburbs map[string]models.SuburbMeta,
	batchSize int,
	onBatch func([]models.StreetDocument) error,
) (map[int]models.RoadAliases, error) {
	if batchSize <= 0 {
		batchSize = 10000
	}

	cr := csv.NewReader(r)
	cr.ReuseRecord = true
	cr.LazyQuotes = true

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read road sections CSV header: %w", err)
	}
	colIdx := buildColumnMap(header)

	batch := make([]models.StreetDocument, 0, batchSize)
	seenStreets := make(map[uint64]struct{}, 64000)
	roadAliases := make(map[int]models.RoadAliases, 64000)
	seq := 0

	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read road sections CSV row: %w", err)
		}

		roadID, err := strconv.Atoi(getColRaw(rec, colIdx, "road_id"))
		if err != nil {
			continue
		}
		roadSectionID, _ := strconv.Atoi(getColRaw(rec, colIdx, "road_section_id"))
		fullRoadNameRaw := getColRaw(rec, colIdx, "full_road_name")
		if fullRoadNameRaw == "" {
			continue
		}
		roadNameLabelRaw := getColRaw(rec, colIdx, "road_name_label")
		secondaryRoadNameRaw := getColRaw(rec, colIdx, "secondary_road_name")
		tertiaryRoadNameRaw := getColRaw(rec, colIdx, "tertiary_road_name")

		// Maintain compact road_id -> RoadAliases map (cloning strings so CSV row buffer can be freed)
		existingAlias := roadAliases[roadID]
		updatedAlias := false
		if existingAlias.RoadNameLabel == "" && roadNameLabelRaw != "" {
			existingAlias.RoadNameLabel = strings.Clone(roadNameLabelRaw)
			updatedAlias = true
		}
		if existingAlias.SecondaryRoadName == "" && secondaryRoadNameRaw != "" {
			existingAlias.SecondaryRoadName = strings.Clone(secondaryRoadNameRaw)
			updatedAlias = true
		}
		if existingAlias.TertiaryRoadName == "" && tertiaryRoadNameRaw != "" {
			existingAlias.TertiaryRoadName = strings.Clone(tertiaryRoadNameRaw)
			updatedAlias = true
		}
		if updatedAlias {
			roadAliases[roadID] = existingAlias
		} else if _, exists := roadAliases[roadID]; !exists {
			roadAliases[roadID] = existingAlias
		}

		suburbRaw := getColRaw(rec, colIdx, "suburb_locality")
		townCityRaw := getColRaw(rec, colIdx, "town_city")

		dedupHash := hashStreetKey(roadID, suburbRaw, townCityRaw)
		if _, exists := seenStreets[dedupHash]; exists {
			continue
		}
		seenStreets[dedupHash] = struct{}{}
		seq++

		suburbLocality := strings.Clone(suburbRaw)
		townCity := strings.Clone(townCityRaw)
		meta := suburbs[strings.ToLower(suburbLocality)]
		geo, _ := ParseWKTToGeo(getShapeColRaw(rec, colIdx))

		docID := fmt.Sprintf("%d_%d", roadID, roadSectionID)
		if roadSectionID == 0 {
			docID = fmt.Sprintf("%d_%d", roadID, seq)
		}

		batch = append(batch, models.StreetDocument{
			ID:                   docID,
			RoadSectionID:        roadSectionID,
			RoadID:               roadID,
			FullRoadName:         strings.Clone(fullRoadNameRaw),
			RoadNameLabel:        existingAlias.RoadNameLabel,
			SecondaryRoadName:    existingAlias.SecondaryRoadName,
			TertiaryRoadName:     existingAlias.TertiaryRoadName,
			SuburbLocality:       suburbLocality,
			SuburbAliases:        meta.AdditionalName,
			TownCity:             townCity,
			TerritorialAuthority: getColCloned(rec, colIdx, "territorial_authority"),
			IsLand:               parseBool(getColRaw(rec, colIdx, "is_land"), true),
			PopulationEstimate:   meta.PopulationEstimate,
			Geo:                  geo,
		})

		if len(batch) >= batchSize {
			if err := onBatch(batch); err != nil {
				return nil, err
			}
			clear(batch)
			batch = batch[:0]
		}
	}

	if len(batch) > 0 {
		if err := onBatch(batch); err != nil {
			return nil, err
		}
		clear(batch)
	}

	return roadAliases, nil
}

func hashStreetKey(roadID int, suburb, town string) uint64 {
	h := fnv.New64a()
	var buf [24]byte
	b := strconv.AppendInt(buf[:0], int64(roadID), 10)
	_, _ = h.Write(b)
	_, _ = h.Write([]byte{'|'})
	_, _ = io.WriteString(h, strings.ToLower(suburb))
	_, _ = h.Write([]byte{'|'})
	_, _ = io.WriteString(h, strings.ToLower(town))
	return h.Sum64()
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

		addressID, err := strconv.Atoi(getColRaw(rec, colIdx, "address_id"))
		if err != nil {
			continue
		}
		roadID, _ := strconv.Atoi(getColRaw(rec, colIdx, "road_id"))
		fullAddrNum := getColCloned(rec, colIdx, "full_address_number")
		fullRoadName := getColCloned(rec, colIdx, "full_road_name")
		fullAddr := getColCloned(rec, colIdx, "full_address")
		ta := getColCloned(rec, colIdx, "territorial_authority")
		unit := getColCloned(rec, colIdx, "unit")
		addrNum, _ := strconv.Atoi(getColRaw(rec, colIdx, "address_number"))
		addrNumSuffix := getColCloned(rec, colIdx, "address_number_suffix")

		var addrNumHigh *int
		if highStr := getColRaw(rec, colIdx, "address_number_high"); highStr != "" {
			if highVal, err := strconv.Atoi(highStr); err == nil {
				addrNumHigh = &highVal
			}
		}

		roadName := getColCloned(rec, colIdx, "road_name")
		roadNameType := getColCloned(rec, colIdx, "road_name_type")
		roadNameSuffix := getColCloned(rec, colIdx, "road_name_suffix")
		suburbLocality := getColCloned(rec, colIdx, "suburb_locality")
		townCity := getColCloned(rec, colIdx, "town_city")
		isLand := parseBool(getColRaw(rec, colIdx, "is_land"), true)
		lifecycle := getColCloned(rec, colIdx, "address_lifecycle")
		if lifecycle == "" {
			lifecycle = "Current"
		}
		shapeStr := getShapeColRaw(rec, colIdx)
		geo, _ := ParseWKTToGeo(shapeStr)

		roadMeta := roads[roadID]
		suburbMeta := suburbs[strings.ToLower(suburbLocality)]

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
			clear(batch)
			batch = batch[:0]
		}
	}

	if len(batch) > 0 {
		if err := onBatch(batch); err != nil {
			return total, err
		}
		clear(batch)
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

// getColRaw returns a trimmed substring slice of the CSV record without allocating.
// Only use for transient parsing (e.g. strconv.Atoi, parseBool, or WKT scanning).
func getColRaw(rec []string, colIdx map[string]int, name string) string {
	idx, ok := colIdx[name]
	if !ok || idx < 0 || idx >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[idx])
}

// getColCloned returns an independently allocated copy of the trimmed CSV column string
// so that retaining the string does not pin the entire CSV row buffer (including large WKT shapes) in memory.
func getColCloned(rec []string, colIdx map[string]int, name string) string {
	val := getColRaw(rec, colIdx, name)
	if val == "" {
		return ""
	}
	return strings.Clone(val)
}

func getShapeColRaw(rec []string, colIdx map[string]int) string {
	for _, candidate := range []string{"shape", "wkt", "geom", "geometry", "the_geom"} {
		if v := getColRaw(rec, colIdx, candidate); v != "" {
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
