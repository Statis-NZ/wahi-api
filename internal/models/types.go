package models

// MeiliGeo represents a geographic coordinate in WGS84 / NZGD2000 for Meilisearch.
// Meilisearch requires "_geo" with "lat" and "lng" keys for geo search,
// while our public REST API exposes "lat" and "lon".
type MeiliGeo struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Location is the public API representation of a geographic coordinate.
type Location struct {
	Lat float64 `json:"lat" doc:"Latitude in decimal degrees (WGS84/NZGD2000)" example:"-36.8485"`
	Lon float64 `json:"lon" doc:"Longitude in decimal degrees (WGS84/NZGD2000)" example:"174.7633"`
}

// LINZSuburbRow represents a raw CSV record from LINZ NZ Suburbs and Localities (Layer 113764).
type LINZSuburbRow struct {
	ID                        int    `json:"id"`
	Name                      string `json:"name"`
	AdditionalName            string `json:"additional_name"`
	Type                      string `json:"type"`
	MajorName                 string `json:"major_name"`
	MajorNameType             string `json:"major_name_type"`
	TerritorialAuthority      string `json:"territorial_authority"`
	PopulationEstimate        int    `json:"population_estimate"`
	NameASCII                 string `json:"name_ascii,omitempty"`
	AdditionalNameASCII       string `json:"additional_name_ascii,omitempty"`
	MajorNameASCII            string `json:"major_name_ascii,omitempty"`
	TerritorialAuthorityASCII string `json:"territorial_authority_ascii,omitempty"`
	ShapeWKT                  string `json:"shape"`
}

// SuburbMeta holds compact in-memory lookup attributes for enriching streets and addresses.
type SuburbMeta struct {
	PopulationEstimate int
	AdditionalName     string
}

// LocalityDocument is the document stored in the Meilisearch "localities" index.
type LocalityDocument struct {
	ID                   int       `json:"id"`
	Name                 string    `json:"name"`
	AdditionalName       string    `json:"additional_name,omitempty"`
	Type                 string    `json:"type,omitempty"`
	MajorName            string    `json:"major_name,omitempty"`
	MajorNameType        string    `json:"major_name_type,omitempty"`
	TerritorialAuthority string    `json:"territorial_authority,omitempty"`
	PopulationEstimate   int       `json:"population_estimate"`
	Geo                  *MeiliGeo `json:"_geo,omitempty"`
}

// LINZRoadSectionRow represents a raw CSV record from LINZ NZ Addresses: Road Sections (Layer 123109).
type LINZRoadSectionRow struct {
	RoadSectionID             int    `json:"road_section_id"`
	RoadID                    int    `json:"road_id"`
	FullRoadName              string `json:"full_road_name"`
	RoadNameLabel             string `json:"road_name_label"`
	RoadNameBody              string `json:"road_name_body,omitempty"`
	RoadNameType              string `json:"road_name_type,omitempty"`
	RoadNameSuffix            string `json:"road_name_suffix,omitempty"`
	SecondaryRoadName         string `json:"secondary_road_name"`
	TertiaryRoadName          string `json:"tertiary_road_name"`
	SuburbLocality            string `json:"suburb_locality"`
	TownCity                  string `json:"town_city"`
	TerritorialAuthority      string `json:"territorial_authority"`
	IsLand                    bool   `json:"is_land"`
	FullRoadNameASCII         string `json:"full_road_name_ascii,omitempty"`
	RoadNameLabelASCII        string `json:"road_name_label_ascii,omitempty"`
	RoadNameBodyASCII         string `json:"road_name_body_ascii,omitempty"`
	SecondaryRoadNameASCII    string `json:"secondary_road_name_ascii,omitempty"`
	TertiaryRoadNameASCII     string `json:"tertiary_road_name_ascii,omitempty"`
	SuburbLocalityASCII       string `json:"suburb_locality_ascii,omitempty"`
	TownCityASCII             string `json:"town_city_ascii,omitempty"`
	TerritorialAuthorityASCII string `json:"territorial_authority_ascii,omitempty"`
	ShapeWKT                  string `json:"shape"`
}

// RoadAliases holds compact in-memory lookup attributes keyed by road_id for enriching addresses.
type RoadAliases struct {
	RoadNameLabel     string
	SecondaryRoadName string
	TertiaryRoadName  string
}

// StreetDocument is the deduplicated street document stored in the Meilisearch "streets" index.
type StreetDocument struct {
	ID                   string    `json:"id"`
	RoadSectionID        int       `json:"road_section_id"`
	RoadID               int       `json:"road_id"`
	FullRoadName         string    `json:"full_road_name"`
	RoadNameLabel        string    `json:"road_name_label,omitempty"`
	SecondaryRoadName    string    `json:"secondary_road_name,omitempty"`
	TertiaryRoadName     string    `json:"tertiary_road_name,omitempty"`
	SuburbLocality       string    `json:"suburb_locality,omitempty"`
	SuburbAliases        string    `json:"suburb_aliases,omitempty"`
	TownCity             string    `json:"town_city,omitempty"`
	TerritorialAuthority string    `json:"territorial_authority,omitempty"`
	IsLand               bool      `json:"is_land"`
	PopulationEstimate   int       `json:"population_estimate"`
	Geo                  *MeiliGeo `json:"_geo,omitempty"`
}

// LINZAddressRow represents a raw CSV record from LINZ NZ Addresses (Layer 123113).
type LINZAddressRow struct {
	AddressID                 int    `json:"address_id"`
	RoadID                    int    `json:"road_id"`
	FullAddressNumber         string `json:"full_address_number"`
	FullRoadName              string `json:"full_road_name"`
	FullAddress               string `json:"full_address"`
	TerritorialAuthority      string `json:"territorial_authority"`
	Unit                      string `json:"unit"`
	AddressNumber             int    `json:"address_number"`
	AddressNumberSuffix       string `json:"address_number_suffix"`
	AddressNumberHigh         *int   `json:"address_number_high,omitempty"`
	RoadName                  string `json:"road_name"`
	RoadNameType              string `json:"road_name_type"`
	RoadNameSuffix            string `json:"road_name_suffix"`
	SuburbLocality            string `json:"suburb_locality"`
	TownCity                  string `json:"town_city"`
	IsLand                    bool   `json:"is_land"`
	AddressLifecycle          string `json:"address_lifecycle"`
	FullRoadNameASCII         string `json:"full_road_name_ascii,omitempty"`
	FullAddressASCII          string `json:"full_address_ascii,omitempty"`
	TerritorialAuthorityASCII string `json:"territorial_authority_ascii,omitempty"`
	RoadNameASCII             string `json:"road_name_ascii,omitempty"`
	SuburbLocalityASCII       string `json:"suburb_locality_ascii,omitempty"`
	TownCityASCII             string `json:"town_city_ascii,omitempty"`
	ShapeWKT                  string `json:"shape"`
}

// AddressDocument is the enriched document stored in the Meilisearch "addresses" index.
type AddressDocument struct {
	AddressID            int       `json:"address_id"`
	RoadID               int       `json:"road_id"`
	FullAddressNumber    string    `json:"full_address_number"`
	FullRoadName         string    `json:"full_road_name"`
	FullAddress          string    `json:"full_address"`
	TerritorialAuthority string    `json:"territorial_authority,omitempty"`
	Unit                 string    `json:"unit,omitempty"`
	AddressNumber        int       `json:"address_number"`
	AddressNumberSuffix  string    `json:"address_number_suffix,omitempty"`
	AddressNumberHigh    *int      `json:"address_number_high,omitempty"`
	RoadName             string    `json:"road_name,omitempty"`
	RoadNameType         string    `json:"road_name_type,omitempty"`
	RoadNameSuffix       string    `json:"road_name_suffix,omitempty"`
	RoadNameLabel        string    `json:"road_name_label,omitempty"`
	SecondaryRoadName    string    `json:"secondary_road_name,omitempty"`
	SuburbLocality       string    `json:"suburb_locality,omitempty"`
	SuburbAliases        string    `json:"suburb_aliases,omitempty"`
	TownCity             string    `json:"town_city,omitempty"`
	IsLand               bool      `json:"is_land"`
	AddressLifecycle     string    `json:"address_lifecycle"`
	PopulationEstimate   int       `json:"population_estimate"`
	Geo                  *MeiliGeo `json:"_geo,omitempty"`
}

// AutosuggestItem represents a single unified result in GET /v1/autosuggest and GET /v1/reverse.
type AutosuggestItem struct {
	ID                int       `json:"id" doc:"Unique identifier (address_id, road_id, or locality id)" example:"3000000"`
	Type              string    `json:"type" doc:"Result type: address, street, or locality" example:"address"`
	FullAddress       string    `json:"full_address" doc:"Formatted display label" example:"1/100A-120A, Queen Street West, Auckland Central, Auckland"`
	FullAddressNumber string    `json:"full_address_number,omitempty" doc:"Formatted unit and street number" example:"1/100A-120A"`
	FullRoadName      string    `json:"full_road_name,omitempty" doc:"Full road name" example:"Queen Street West"`
	SuburbLocality    string    `json:"suburb_locality,omitempty" doc:"Suburb or locality name" example:"Auckland Central"`
	TownCity          string    `json:"town_city,omitempty" doc:"Town or city name" example:"Auckland"`
	Location          *Location `json:"location,omitempty" doc:"Geographic coordinates"`
}

// AddressDetailResponse represents the full address document returned by GET /v1/addresses/{address_id}.
type AddressDetailResponse struct {
	AddressID            int       `json:"address_id" doc:"LINZ primary address identifier" example:"3000000"`
	RoadID               int       `json:"road_id" doc:"LINZ road identifier" example:"10500"`
	FullAddress          string    `json:"full_address" doc:"Full formatted address" example:"1/100A-120A, Queen Street West, Auckland Central, Auckland"`
	FullAddressNumber    string    `json:"full_address_number" doc:"Full formatted street number including unit and range" example:"1/100A-120A"`
	FullRoadName         string    `json:"full_road_name" doc:"Full road name" example:"Queen Street West"`
	Unit                 string    `json:"unit,omitempty" doc:"Unit, flat, or apartment identifier" example:"1"`
	AddressNumber        int       `json:"address_number" doc:"Base street number" example:"100"`
	AddressNumberSuffix  string    `json:"address_number_suffix,omitempty" doc:"Street number suffix" example:"A"`
	AddressNumberHigh    *int      `json:"address_number_high,omitempty" doc:"Upper street number for ranged addresses" example:"120"`
	RoadName             string    `json:"road_name,omitempty" doc:"Core road name" example:"Queen"`
	RoadNameType         string    `json:"road_name_type,omitempty" doc:"Road type (e.g., Street, Road)" example:"Street"`
	RoadNameSuffix       string    `json:"road_name_suffix,omitempty" doc:"Road directional or other suffix" example:"West"`
	RoadNameLabel        string    `json:"road_name_label,omitempty" doc:"Abbreviated road label" example:"Queen St W"`
	SecondaryRoadName    string    `json:"secondary_road_name,omitempty" doc:"Secondary road name (e.g. State Highway)" example:"State Highway 1"`
	SuburbLocality       string    `json:"suburb_locality,omitempty" doc:"Suburb or locality" example:"Auckland Central"`
	TownCity             string    `json:"town_city,omitempty" doc:"Town or city" example:"Auckland"`
	TerritorialAuthority string    `json:"territorial_authority,omitempty" doc:"Territorial Authority" example:"Auckland"`
	IsLand               bool      `json:"is_land" doc:"True if land-based address, false if water-based" example:"true"`
	AddressLifecycle     string    `json:"address_lifecycle" doc:"Lifecycle stage (Current or Proposed)" example:"Current"`
	Location             *Location `json:"location,omitempty" doc:"Geographic coordinates"`
}
