//go:build codegen
// +build codegen

package api

// IoSuffix represents map of service to shape names that
// are suffixed with `Input`, `Output` string and are not
// Input or Output shapes used by any operation within
// the service enclosure.
type IoSuffix map[string]map[string]struct{}

// LegacyIoSuffix returns if the shape names are legacy
// names that contain "Input" and "Output" name as suffix.
func (i IoSuffix) LegacyIOSuffix(a *API, shapeName string) bool {
	names, ok := i[a.name]
	if !ok {
		return false
	}

	_, ok = names[shapeName]
	return ok
}

// legacyIOSuffixed is the list of known shapes that have "Input" and "Output"
// as suffix in shape name, but are not the actual input, output shape
// for a corresponding service operation.
var legacyIOSuffixed = IoSuffix{
	"S3": {
		"ParquetInput": struct{}{},
		"CSVOutput":    struct{}{},
		"JSONOutput":   struct{}{},
		"JSONInput":    struct{}{},
		"CSVInput":     struct{}{},
	},
}
