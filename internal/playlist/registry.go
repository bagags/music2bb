package playlist

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// IdentificationRegistration associates one provider identifier with its
// stable provider ID.
type IdentificationRegistration struct {
	ProviderID ProviderID
	Identifier Identifier
}

// IdentificationRegistry is an immutable ordered set of provider
// identifiers.
type IdentificationRegistry struct {
	entries []IdentificationRegistration
}

// NewIdentificationRegistry constructs an immutable identification registry.
func NewIdentificationRegistry(entries ...IdentificationRegistration) (*IdentificationRegistry, error) {
	cloned := append([]IdentificationRegistration(nil), entries...)
	for index, entry := range cloned {
		if stringsEmpty(string(entry.ProviderID)) {
			return nil, fmt.Errorf("identification registration %d has no provider ID", index)
		}
		if nilInterface(entry.Identifier) {
			return nil, fmt.Errorf("identification registration %d has no identifier", index)
		}
	}
	return &IdentificationRegistry{entries: cloned}, nil
}

// Identify returns the generic provider when no identifier matches and an
// error when multiple registrations match the same URL.
func (r *IdentificationRegistry) Identify(source Source) (ProviderID, error) {
	if r == nil {
		return GenericProviderID, nil
	}
	matched := make([]ProviderID, 0, 2)
	for _, entry := range r.entries {
		if entry.Identifier.MatchesURL(source.URL()) {
			matched = append(matched, entry.ProviderID)
		}
	}
	switch len(matched) {
	case 0:
		return GenericProviderID, nil
	case 1:
		return matched[0], nil
	default:
		return "", &RegistrationError{ProviderIDs: matched}
	}
}

// OptimizationRegistration associates independently constructed typed
// optimizations with a provider ID.
type OptimizationRegistration struct {
	ProviderID    ProviderID
	Optimizations ProviderOptimizations
}

// OptimizationRegistry is an immutable provider optimization lookup.
type OptimizationRegistry struct {
	providers map[ProviderID]ProviderOptimizations
}

// NewOptimizationRegistry constructs an immutable optimization registry.
func NewOptimizationRegistry(entries ...OptimizationRegistration) (*OptimizationRegistry, error) {
	providers := make(map[ProviderID]ProviderOptimizations, len(entries))
	for index, entry := range entries {
		if stringsEmpty(string(entry.ProviderID)) {
			return nil, fmt.Errorf("optimization registration %d has no provider ID", index)
		}
		if _, exists := providers[entry.ProviderID]; exists {
			return nil, fmt.Errorf("provider %q has multiple optimization registrations", entry.ProviderID)
		}
		if err := validateOptimizations(entry.ProviderID, entry.Optimizations); err != nil {
			return nil, err
		}
		providers[entry.ProviderID] = cloneOptimizations(entry.Optimizations)
	}
	return &OptimizationRegistry{providers: providers}, nil
}

func validateOptimizations(providerID ProviderID, value ProviderOptimizations) error {
	for index, extractor := range value.PlaylistExtractors {
		if nilInterface(extractor) {
			return fmt.Errorf("provider %q playlist extractor %d is nil", providerID, index)
		}
	}
	for index, extractor := range value.TitleExtractors {
		if nilInterface(extractor) {
			return fmt.Errorf("provider %q title extractor %d is nil", providerID, index)
		}
	}
	for index, normalizer := range value.SongNormalizers {
		if nilInterface(normalizer) {
			return fmt.Errorf("provider %q song normalizer %d is nil", providerID, index)
		}
	}
	return nil
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// Lookup returns a caller-owned copy of the provider's registered slices. A
// known provider with no optimizations and an unregistered provider both
// produce empty categories.
func (r *OptimizationRegistry) Lookup(providerID ProviderID) ProviderOptimizations {
	if r == nil {
		return ProviderOptimizations{}
	}
	return cloneOptimizations(r.providers[providerID])
}

func cloneOptimizations(value ProviderOptimizations) ProviderOptimizations {
	return ProviderOptimizations{
		PlaylistExtractors: append([]PlaylistExtractor(nil), value.PlaylistExtractors...),
		TitleExtractors:    append([]TitleExtractor(nil), value.TitleExtractors...),
		SongNormalizers:    append([]SongNormalizer(nil), value.SongNormalizers...),
	}
}

func stringsEmpty(value string) bool { return strings.TrimSpace(value) == "" }

// RegistrationError reports ambiguous provider identification.
type RegistrationError struct {
	ProviderIDs []ProviderID
}

func (e *RegistrationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("multiple provider identifiers matched: %v", e.ProviderIDs)
}

// IsRegistrationError reports whether err contains a registration error.
func IsRegistrationError(err error) bool {
	var target *RegistrationError
	return errors.As(err, &target)
}
