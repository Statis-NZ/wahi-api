package api

import (
	"testing"
)

func intPtr(v int) *int {
	return &v
}

func TestParseAddressQuery(t *testing.T) {
	tests := []struct {
		name            string
		query           string
		wantHasNumber   bool
		wantUnit        string
		wantNumber      int
		wantSuffix      string
		wantNumberHigh  *int
		wantStreetQuery string
		wantFilter      string
	}{
		{
			name:            "simple street number and road",
			query:           "110 Queen St",
			wantHasNumber:   true,
			wantNumber:      110,
			wantStreetQuery: "Queen St",
			wantFilter:      "(address_number = 110 OR (address_number <= 110 AND address_number_high >= 110))",
		},
		{
			name:            "slash unit and street number with suffix",
			query:           "2/100A Queen St",
			wantHasNumber:   true,
			wantUnit:        "2",
			wantNumber:      100,
			wantSuffix:      "A",
			wantStreetQuery: "Queen St",
			wantFilter:      "(address_number = 100 OR (address_number <= 100 AND address_number_high >= 100))",
		},
		{
			name:            "unit, range, and suffixes",
			query:           "1/100A-120A, Queen Street West, Auckland Central",
			wantHasNumber:   true,
			wantUnit:        "1",
			wantNumber:      100,
			wantSuffix:      "A",
			wantNumberHigh:  intPtr(120),
			wantStreetQuery: "Queen Street West, Auckland Central",
			wantFilter:      "(address_number = 100 OR (address_number <= 100 AND address_number_high >= 100))",
		},
		{
			name:            "word unit prefix with comma",
			query:           "Unit 4b, 250 Lambton Quay",
			wantHasNumber:   true,
			wantUnit:        "4B",
			wantNumber:      250,
			wantStreetQuery: "Lambton Quay",
			wantFilter:      "(address_number = 250 OR (address_number <= 250 AND address_number_high >= 250))",
		},
		{
			name:            "flat prefix with space",
			query:           "Flat 2 15B Ponsonby Rd",
			wantHasNumber:   true,
			wantUnit:        "2",
			wantNumber:      15,
			wantSuffix:      "B",
			wantStreetQuery: "Ponsonby Rd",
			wantFilter:      "(address_number = 15 OR (address_number <= 15 AND address_number_high >= 15))",
		},
		{
			name:            "apt prefix with slash",
			query:           "Apt 12/88 Broadway, Newmarket",
			wantHasNumber:   true,
			wantUnit:        "12",
			wantNumber:      88,
			wantStreetQuery: "Broadway, Newmarket",
			wantFilter:      "(address_number = 88 OR (address_number <= 88 AND address_number_high >= 88))",
		},
		{
			name:            "number only query",
			query:           "100",
			wantHasNumber:   true,
			wantNumber:      100,
			wantStreetQuery: "",
			wantFilter:      "(address_number = 100 OR (address_number <= 100 AND address_number_high >= 100))",
		},
		{
			name:            "street only query without number",
			query:           "Ponsonby Rd",
			wantHasNumber:   false,
			wantStreetQuery: "Ponsonby Rd",
			wantFilter:      "",
		},
		{
			name:            "suburb with macron",
			query:           "Ōtara",
			wantHasNumber:   false,
			wantStreetQuery: "Ōtara",
			wantFilter:      "",
		},
		{
			name:            "state highway without leading street number",
			query:           "State Highway 45",
			wantHasNumber:   false,
			wantStreetQuery: "State Highway 45",
			wantFilter:      "",
		},
		{
			name:            "empty query",
			query:           "   ",
			wantHasNumber:   false,
			wantStreetQuery: "",
			wantFilter:      "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseAddressQuery(tc.query)
			if got.HasStreetNumber != tc.wantHasNumber {
				t.Fatalf("HasStreetNumber = %v, want %v", got.HasStreetNumber, tc.wantHasNumber)
			}
			if got.Unit != tc.wantUnit {
				t.Errorf("Unit = %q, want %q", got.Unit, tc.wantUnit)
			}
			if got.Number != tc.wantNumber {
				t.Errorf("Number = %d, want %d", got.Number, tc.wantNumber)
			}
			if got.NumberSuffix != tc.wantSuffix {
				t.Errorf("NumberSuffix = %q, want %q", got.NumberSuffix, tc.wantSuffix)
			}
			if (got.NumberHigh == nil) != (tc.wantNumberHigh == nil) {
				t.Errorf("NumberHigh = %v, want %v", got.NumberHigh, tc.wantNumberHigh)
			} else if got.NumberHigh != nil && *got.NumberHigh != *tc.wantNumberHigh {
				t.Errorf("*NumberHigh = %d, want %d", *got.NumberHigh, *tc.wantNumberHigh)
			}
			if got.StreetQuery != tc.wantStreetQuery {
				t.Errorf("StreetQuery = %q, want %q", got.StreetQuery, tc.wantStreetQuery)
			}
			if filter := got.BuildAddressNumberFilter(); filter != tc.wantFilter {
				t.Errorf("BuildAddressNumberFilter() = %q, want %q", filter, tc.wantFilter)
			}
		})
	}
}
