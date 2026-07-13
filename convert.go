package music2bb

import (
	"github.com/gguage/music-to-bb/internal/model"
	"github.com/gguage/music-to-bb/internal/service"
)

func songFromInternal(song model.Song) Song {
	return Song{Name: song.Name, Artist: song.Artist, Album: song.Album, Duration: song.Duration, Hash: song.Hash}
}

func songToInternal(song Song) model.Song {
	return model.Song{Name: song.Name, Artist: song.Artist, Album: song.Album, Duration: song.Duration, Hash: song.Hash}
}

func songsFromInternal(songs []model.Song) []Song {
	result := make([]Song, len(songs))
	for index, song := range songs {
		result[index] = songFromInternal(song)
	}
	return result
}

func songsToInternal(songs []Song) []model.Song {
	result := make([]model.Song, len(songs))
	for index, song := range songs {
		result[index] = songToInternal(song)
	}
	return result
}

func videoFromInternal(video model.Video) Video {
	return Video{
		BVID: video.BVID, AID: video.AID, Title: video.Title, Uploader: video.Uploader,
		Duration: video.Duration, PlayCount: video.PlayCount, FavoriteCount: video.FavoriteCount,
		DanmakuCount: video.DanmakuCount, Description: video.Description,
		Tags: append([]string(nil), video.Tags...), IsOfficial: video.IsOfficial, IsVerified: video.IsVerified,
	}
}

func videoToInternal(video Video) model.Video {
	return model.Video{
		BVID: video.BVID, AID: video.AID, Title: video.Title, Uploader: video.Uploader,
		Duration: video.Duration, PlayCount: video.PlayCount, FavoriteCount: video.FavoriteCount,
		DanmakuCount: video.DanmakuCount, Description: video.Description,
		Tags: append([]string(nil), video.Tags...), IsOfficial: video.IsOfficial, IsVerified: video.IsVerified,
	}
}

func candidateFromInternal(match model.MatchResult) MatchResult {
	result := MatchResult{
		Song: songFromInternal(match.Song), Score: match.Score,
		KeywordScore: match.KeywordScore, QualityScore: match.QualityScore,
		OfficialScore: match.OfficialScore, PopularityScore: match.PopularityScore,
		UploaderScore: match.UploaderScore, Matched: match.Matched,
		HasSelection: match.Video != nil, ManualOverride: match.ManualOverride,
	}
	if match.Video != nil {
		video := videoFromInternal(*match.Video)
		result.Video = &video
	}
	return result
}

func candidateToInternal(match MatchResult) model.MatchResult {
	result := model.MatchResult{
		Song: songToInternal(match.Song), Score: match.Score,
		KeywordScore: match.KeywordScore, QualityScore: match.QualityScore,
		OfficialScore: match.OfficialScore, PopularityScore: match.PopularityScore,
		UploaderScore: match.UploaderScore, Matched: match.Matched,
		ManualOverride: match.ManualOverride,
	}
	if match.Video != nil {
		video := videoToInternal(*match.Video)
		result.Video = &video
	}
	return result
}

func candidatesFromInternal(matches []model.MatchResult) []MatchResult {
	result := make([]MatchResult, len(matches))
	for index, match := range matches {
		result[index] = candidateFromInternal(match)
	}
	return result
}

func outcomesFromInternal(outcomes []service.MatchOutcome) []MatchResult {
	result := make([]MatchResult, len(outcomes))
	for index, outcome := range outcomes {
		converted := candidateFromInternal(outcome.Selected)
		converted.Song = songFromInternal(outcome.Song)
		converted.HasSelection = outcome.HasSelection
		converted.ManualOverride = outcome.ManualOverride || converted.ManualOverride
		converted.Candidates = candidatesFromInternal(outcome.Candidates)
		if outcome.Failure != nil {
			converted.Failure = &ItemFailure{Index: outcome.Failure.Index, Operation: outcome.Failure.Operation, Item: outcome.Failure.Item, Reason: outcome.Failure.Reason}
		}
		result[index] = converted
	}
	return result
}

func outcomeToInternal(match MatchResult) service.MatchOutcome {
	candidate := candidateToInternal(match)
	outcome := service.MatchOutcome{
		Song: songToInternal(match.Song), Selected: candidate,
		HasSelection: match.HasSelection, ManualOverride: match.ManualOverride,
	}
	if match.Failure != nil {
		outcome.Failure = &service.ItemFailure{Index: match.Failure.Index, Operation: match.Failure.Operation, Item: match.Failure.Item, Reason: match.Failure.Reason}
	}
	for _, publicCandidate := range match.Candidates {
		outcome.Candidates = append(outcome.Candidates, candidateToInternal(publicCandidate))
	}
	return outcome
}

func favoriteFromInternal(favorite model.Favorite) Favorite {
	return Favorite{ID: favorite.ID, Title: favorite.Title, Count: favorite.Count, MediaCount: favorite.MediaCount}
}

func favoritesFromInternal(favorites []model.Favorite) []Favorite {
	result := make([]Favorite, len(favorites))
	for index, favorite := range favorites {
		result[index] = favoriteFromInternal(favorite)
	}
	return result
}

func observerAdapter(observer Observer) service.Observer {
	if observer == nil {
		return nil
	}
	return service.ObserverFunc(func(event service.ProgressEvent) {
		converted := ProgressEvent{
			Kind: EventKind(event.Kind), Operation: event.Operation, Message: event.Message,
			Current: event.Current, Total: event.Total, QRPayload: event.QRPayload, At: event.At,
		}
		if event.Song != nil {
			song := songFromInternal(*event.Song)
			converted.Song = &song
		}
		if event.Match != nil {
			match := candidateFromInternal(*event.Match)
			converted.Match = &match
		}
		observer.Observe(converted)
	})
}
