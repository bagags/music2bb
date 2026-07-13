package playlist

import (
	"context"
	"errors"
	"fmt"

	"github.com/gguage/music-to-bb/internal/model"
)

// Coordinator routes a validated source through provider optimizations and,
// when policy permits, the generic browser fallback.
type Coordinator struct {
	identification *IdentificationRegistry
	optimizations  *OptimizationRegistry
	browser        BrowserExtractor
}

// NewCoordinator assembles a neutral playlist ingestion coordinator.
func NewCoordinator(identification *IdentificationRegistry, optimizations *OptimizationRegistry, browser BrowserExtractor) *Coordinator {
	return &Coordinator{identification: identification, optimizations: optimizations, browser: browser}
}

// ParsePlaylist extracts and decodes one playlist according to browser policy.
func (c *Coordinator) ParsePlaylist(ctx context.Context, rawURL string, policy BrowserPolicy) (Result, error) {
	source, err := ParseSource(rawURL)
	if err != nil {
		return Result{}, &Error{Kind: ErrorInvalidInput, Op: "parse URL", Err: err}
	}
	if policy == "" {
		policy = BrowserAuto
	}
	if policy != BrowserAuto && policy != BrowserNever && policy != BrowserAlways {
		return Result{}, &Error{Kind: ErrorInvalidInput, Op: "parse playlist", Message: "invalid browser policy"}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	providerID, err := c.identification.Identify(source)
	if err != nil {
		return Result{}, &Error{Kind: ErrorInternal, Op: "identify provider", Err: err}
	}
	optimizations := c.optimizations.Lookup(providerID)

	browserAvailable := false
	availabilityChecked := false
	checkBrowser := func() error {
		availabilityChecked = true
		if c.browser == nil {
			browserAvailable = false
			return nil
		}
		available, availabilityErr := c.browser.Available(ctx)
		if contextError(ctx, availabilityErr) != nil {
			return contextError(ctx, availabilityErr)
		}
		if availabilityErr != nil {
			return &Error{Kind: ErrorBrowser, Op: "browser status", Err: availabilityErr}
		}
		browserAvailable = available
		return nil
	}
	if policy == BrowserAlways {
		if err := checkBrowser(); err != nil {
			return Result{}, err
		}
		if !browserAvailable {
			return Result{}, &Error{Kind: ErrorBrowser, Op: "parse playlist", Message: "verified browser is not installed"}
		}
	}

	result := Result{}
	providerComplete := false
	var failures []error
	for _, extractor := range optimizations.PlaylistExtractors {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		raw, attemptErr := extractor.ExtractPlaylist(ctx, source)
		if err := contextError(ctx, attemptErr); err != nil {
			return Result{}, err
		}
		decoded := normalizeSongs(DecodeTracks(raw.Tracks, optimizations.TitleExtractors), optimizations.SongNormalizers)
		if attemptErr != nil {
			failures = append(failures, &AttemptError{
				ProviderID: providerID, Category: CapabilityPlaylistExtraction,
				Optimization: optimizationName(extractor), Err: attemptErr,
			})
		}
		if len(decoded) > 0 && (raw.ExpectedTotal == 0 || len(decoded) >= raw.ExpectedTotal) {
			result.Songs = decoded
			result.ExpectedTotal = raw.ExpectedTotal
			providerComplete = true
			break
		}
		result.Songs = MergeSongs(result.Songs, decoded)
		if raw.ExpectedTotal > result.ExpectedTotal {
			result.ExpectedTotal = raw.ExpectedTotal
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	needsBrowser := !providerComplete && (len(result.Songs) == 0 || (result.ExpectedTotal > len(result.Songs)))
	if !needsBrowser {
		return cloneResult(result), nil
	}
	if policy == BrowserNever {
		if len(result.Songs) > 0 {
			return cloneResult(result), nil
		}
		return Result{}, extractionError(failures, "no provider playlist optimization returned songs")
	}
	if !availabilityChecked {
		if err := checkBrowser(); err != nil {
			if len(result.Songs) > 0 {
				return cloneResult(result), nil
			}
			if len(optimizations.PlaylistExtractors) > 0 {
				failures = append(failures, err)
				return Result{}, extractionError(failures, "provider extraction failed and browser status is unavailable")
			}
			return Result{}, err
		}
	}
	if !browserAvailable {
		if len(result.Songs) > 0 {
			return cloneResult(result), nil
		}
		if len(optimizations.PlaylistExtractors) > 0 {
			return Result{}, extractionError(failures, "provider extraction returned no songs and browser is unavailable")
		}
		return Result{}, &Error{Kind: ErrorBrowser, Op: "parse playlist", Message: "verified browser is not installed"}
	}

	raw, browserErr := c.browser.ExtractPlaylist(ctx, source)
	if err := contextError(ctx, browserErr); err != nil {
		return Result{}, err
	}
	browserSongs := normalizeSongs(DecodeTracks(raw.Tracks, optimizations.TitleExtractors), optimizations.SongNormalizers)
	result.Songs = MergeSongs(result.Songs, browserSongs)
	if raw.ExpectedTotal > result.ExpectedTotal {
		result.ExpectedTotal = raw.ExpectedTotal
	}
	if browserErr != nil {
		failures = append(failures, &AttemptError{
			ProviderID: providerID, Category: CapabilityBrowserExtraction,
			Optimization: "controlled-browser", Err: browserErr,
		})
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if len(result.Songs) > 0 {
		return cloneResult(result), nil
	}
	return Result{}, extractionError(failures, "no playlist extraction returned songs")
}

func extractionError(failures []error, fallback string) error {
	if len(failures) == 0 {
		failures = append(failures, errors.New(fallback))
	}
	return &Error{Kind: ErrorExtraction, Op: "parse playlist", Err: errors.Join(failures...)}
}

func contextError(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if errors.Is(fallback, context.Canceled) || errors.Is(fallback, context.DeadlineExceeded) {
		return fallback
	}
	return nil
}

func optimizationName(value Optimization) string {
	if value == nil || value.Name() == "" {
		return fmt.Sprintf("%T", value)
	}
	return value.Name()
}

func cloneResult(value Result) Result {
	return Result{Songs: append([]model.Song(nil), value.Songs...), ExpectedTotal: value.ExpectedTotal}
}
