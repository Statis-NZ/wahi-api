package api

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// addressQueryRegex matches optional leading unit/flat patterns and street numbers:
// Examples:
// - "2/100A Queen St"
// - "1/100A-120A Queen Street West"
// - "Unit 3B, 45 Ponsonby Road"
// - "Flat 2 15B Lambton Quay"
// - "110 Queen St"
var addressQueryRegex = regexp.MustCompile(
	`(?i)^(?:(?:unit|flat|apt|u|f)\s*([a-zA-Z0-9]+)[,\s/]+|([a-zA-Z0-9]+)\s*/\s*)?(\d+)([a-zA-Z])?(?:\s*-\s*(\d+)([a-zA-Z])?)?\b[,\s]*(.*)$`,
)

var roadTypeTokens = map[string]struct{}{
	"st": {}, "street": {},
	"rd": {}, "road": {},
	"ave": {}, "av": {}, "avenue": {},
	"dr": {}, "drive": {},
	"pl": {}, "place": {},
	"cres": {}, "cr": {}, "crescent": {},
	"tce": {}, "ter": {}, "terrace": {},
	"hwy": {}, "highway": {},
	"ln": {}, "lane": {},
	"ct": {}, "crt": {}, "court": {},
	"way":  {},
	"quay": {}, "qy": {},
	"grv": {}, "grove": {},
	"cl": {}, "close": {},
	"blvd": {}, "bvd": {}, "boulevard": {},
	"esp": {}, "esplanade": {},
	"pde": {}, "parade": {},
	"mews": {},
	"rise": {},
	"wk":   {}, "walk": {},
	"trk": {}, "track": {},
	"cct": {}, "circuit": {},
	"loop": {},
	"sq":   {}, "square": {},
	"mall": {},
	"al":   {}, "alley": {},
	"fwy": {}, "freeway": {},
	"mwy": {}, "motorway": {},
	"pkwy": {}, "parkway": {},
	"cswy": {}, "causeway": {},
	"brae": {},
}

// hasRoadTypeToken checks whether a query contains an unambiguous road type suffix
// (e.g. "Queen St", "Ponsonby Road", "St Lukes Rd") or highway prefix ("SH 1", "State Highway 45").
// Leading "St" (e.g. "St Heliers") at token index 0 is treated as "Saint", not a road type.
func hasRoadTypeToken(s string) bool {
	tokens := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r == ' ' || r == '\t' || r == ',' || r == '.'
	})
	if len(tokens) < 2 {
		return false
	}
	if tokens[0] == "sh" || (tokens[0] == "state" && (tokens[1] == "highway" || tokens[1] == "hwy")) {
		return true
	}
	for _, tok := range tokens[1:] {
		if _, ok := roadTypeTokens[tok]; ok {
			return true
		}
	}
	return false
}

// ParsedQuery holds the structured components extracted from an address search query.
type ParsedQuery struct {
	Raw             string
	HasStreetNumber bool
	HasRoadType     bool
	Unit            string
	Number          int
	NumberSuffix    string
	NumberHigh      *int
	NumberHighSuf   string
	StreetQuery     string
}

// ParseAddressQuery extracts NZ unit numbers, street numbers, suffixes, ranges,
// and road type indicators from a raw search string.
func ParseAddressQuery(q string) ParsedQuery {
	trimmed := strings.TrimSpace(q)
	if trimmed == "" {
		return ParsedQuery{Raw: q}
	}

	matches := addressQueryRegex.FindStringSubmatch(trimmed)
	if matches == nil {
		return ParsedQuery{
			Raw:             trimmed,
			HasStreetNumber: false,
			HasRoadType:     hasRoadTypeToken(trimmed),
			StreetQuery:     trimmed,
		}
	}

	unit := matches[1]
	if unit == "" {
		unit = matches[2]
	}
	unit = strings.ToUpper(strings.TrimSpace(unit))

	num, err := strconv.Atoi(matches[3])
	if err != nil {
		return ParsedQuery{
			Raw:             trimmed,
			HasStreetNumber: false,
			HasRoadType:     hasRoadTypeToken(trimmed),
			StreetQuery:     trimmed,
		}
	}

	suffix := strings.ToUpper(strings.TrimSpace(matches[4]))

	var numHigh *int
	if matches[5] != "" {
		if nh, err := strconv.Atoi(matches[5]); err == nil {
			numHigh = &nh
		}
	}
	highSuffix := strings.ToUpper(strings.TrimSpace(matches[6]))
	remaining := strings.TrimSpace(matches[7])

	return ParsedQuery{
		Raw:             trimmed,
		HasStreetNumber: true,
		HasRoadType:     hasRoadTypeToken(remaining),
		Unit:            unit,
		Number:          num,
		NumberSuffix:    suffix,
		NumberHigh:      numHigh,
		NumberHighSuf:   highSuffix,
		StreetQuery:     remaining,
	}
}

// BuildAddressNumberFilter returns the Meilisearch filter expression that matches both
// exact street numbers and ranged addresses covering the queried street number.
func (p ParsedQuery) BuildAddressNumberFilter() string {
	if !p.HasStreetNumber {
		return ""
	}
	if p.NumberHigh != nil {
		return p.BuildRangeAddressNumberFilter()
	}
	return fmt.Sprintf(
		"(%s OR %s)",
		p.BuildExactAddressNumberFilter(),
		p.BuildRangeAddressNumberFilter(),
	)
}

// BuildExactAddressNumberFilter returns the Meilisearch filter expression for an exact street number match.
func (p ParsedQuery) BuildExactAddressNumberFilter() string {
	if !p.HasStreetNumber {
		return ""
	}
	return fmt.Sprintf("address_number = %d", p.Number)
}

// BuildRangeAddressNumberFilter returns the Meilisearch filter expression for ranged addresses
// covering the queried street number or range.
func (p ParsedQuery) BuildRangeAddressNumberFilter() string {
	if !p.HasStreetNumber {
		return ""
	}
	high := p.Number
	if p.NumberHigh != nil {
		high = *p.NumberHigh
	}
	return fmt.Sprintf("(address_number <= %d AND address_number_high >= %d)", p.Number, high)
}
