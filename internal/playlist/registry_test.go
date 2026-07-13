package playlist

import (
	"net/url"
	"reflect"
	"testing"
)

func TestParseSourceValidatesHTTPURLs(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		wantErr bool
	}{
		{name: "https", rawURL: " https://Example.test/list ", wantErr: false},
		{name: "http", rawURL: "http://example.test", wantErr: false},
		{name: "relative", rawURL: "example.test/list", wantErr: true},
		{name: "hostless", rawURL: "https:///list", wantErr: true},
		{name: "unsupported", rawURL: "file:///tmp/list", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, err := ParseSource(test.rawURL)
			if (err != nil) != test.wantErr {
				t.Fatalf("ParseSource(%q) error = %v", test.rawURL, err)
			}
			if err == nil && source.URL().Hostname() == "" {
				t.Fatalf("source = %#v", source)
			}
		})
	}
}

func TestIdentificationRegistryUnknownKnownAndAmbiguous(t *testing.T) {
	first := IdentifierFunc(func(value *url.URL) bool { return value.Hostname() == "known.test" })
	second := IdentifierFunc(func(value *url.URL) bool { return value.Path == "/ambiguous" })
	registry, err := NewIdentificationRegistry(
		IdentificationRegistration{ProviderID: "known", Identifier: first},
		IdentificationRegistration{ProviderID: "other", Identifier: second},
	)
	if err != nil {
		t.Fatal(err)
	}
	unknown, _ := ParseSource("https://unknown.test/list")
	if got, err := registry.Identify(unknown); err != nil || got != GenericProviderID {
		t.Fatalf("unknown Identify = %q, %v", got, err)
	}
	known, _ := ParseSource("https://known.test/list")
	if got, err := registry.Identify(known); err != nil || got != "known" {
		t.Fatalf("known Identify = %q, %v", got, err)
	}
	ambiguous, _ := ParseSource("https://known.test/ambiguous")
	if _, err := registry.Identify(ambiguous); !IsRegistrationError(err) {
		t.Fatalf("ambiguous Identify error = %v", err)
	}
}

type pointerIdentifier struct{}

func (*pointerIdentifier) MatchesURL(*url.URL) bool { return false }

func TestIdentificationRegistryRejectsTypedNilIdentifier(t *testing.T) {
	var identifier *pointerIdentifier
	if _, err := NewIdentificationRegistry(IdentificationRegistration{ProviderID: "known", Identifier: identifier}); err == nil {
		t.Fatal("NewIdentificationRegistry accepted a typed nil identifier")
	}
}

func TestOptimizationRegistriesAreIndependentAndImmutable(t *testing.T) {
	title := NewFieldTitleExtractor(FieldTitleOptions{OptimizationName: "shared", TitleKeys: []string{"title"}})
	registered := ProviderOptimizations{TitleExtractors: []TitleExtractor{title}}
	registry, err := NewOptimizationRegistry(
		OptimizationRegistration{ProviderID: "one", Optimizations: registered},
		OptimizationRegistration{ProviderID: "two", Optimizations: ProviderOptimizations{TitleExtractors: []TitleExtractor{title}}},
		OptimizationRegistration{ProviderID: "known-empty", Optimizations: ProviderOptimizations{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	registered.TitleExtractors[0] = nil
	one := registry.Lookup("one")
	two := registry.Lookup("two")
	if len(one.TitleExtractors) != 1 || one.TitleExtractors[0] != title || two.TitleExtractors[0] != title {
		t.Fatalf("shared registrations were not retained: %#v %#v", one, two)
	}
	one.TitleExtractors[0] = nil
	if got := registry.Lookup("one"); got.TitleExtractors[0] != title {
		t.Fatal("lookup exposed the registry slice")
	}
	if got := registry.Lookup("known-empty"); !reflect.DeepEqual(got, ProviderOptimizations{}) {
		t.Fatalf("known empty = %#v", got)
	}
}

func TestOptimizationRegistryRejectsNilCapabilities(t *testing.T) {
	var typedNil *fakePlaylistExtractor
	for name, optimizations := range map[string]ProviderOptimizations{
		"playlist":   {PlaylistExtractors: []PlaylistExtractor{nil}},
		"typed nil":  {PlaylistExtractors: []PlaylistExtractor{typedNil}},
		"title":      {TitleExtractors: []TitleExtractor{nil}},
		"normalizer": {SongNormalizers: []SongNormalizer{nil}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewOptimizationRegistry(OptimizationRegistration{ProviderID: "provider", Optimizations: optimizations}); err == nil {
				t.Fatal("NewOptimizationRegistry accepted a nil capability")
			}
		})
	}
}
