package apicompat

// RequestConversionOptions controls optional text synthesis during protocol conversion.
// The zero value preserves the existing compatibility behavior.
type RequestConversionOptions struct {
	// PreserveClientText retains caller text without generated descriptions,
	// empty-result placeholders, reasoning tags, or instruction whitespace trimming.
	PreserveClientText bool
}
