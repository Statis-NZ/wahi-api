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

// ParsedQuery holds the structured components extracted from an address search query.
type ParsedQuery struct {
	Raw             string
	HasStreetNumber bool
	Unit            string
	Number          int
	NumberSuffix    string
	NumberHigh      *int
	NumberHighSuf   string
	StreetQuery     string
}

// ParseAddressQuery extracts NZ unit numbers, street numbers, suffixes, and ranges
// from a raw search string.
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
	return fmt.Sprintf(
		"(address_number = %d OR (address_number <= %d AND address_number_high >= %d))",
		p.Number, p.Number, p.Number,
	)
}
