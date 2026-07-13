package service

import (
	"sync"
	"time"

	"github.com/gguage/music-to-bb/internal/model"
)

type EventKind string

const (
	EventProgress EventKind = "progress"
	EventWarning  EventKind = "warning"
	EventQR       EventKind = "qr"
	EventSong     EventKind = "song"
	EventVideo    EventKind = "video"
)

type ProgressEvent struct {
	Kind      EventKind
	Operation string
	Message   string
	Current   int
	Total     int
	Song      *model.Song
	Match     *model.MatchResult
	QRPayload string
	At        time.Time
}

type Observer interface {
	Observe(ProgressEvent)
}

type ObserverFunc func(ProgressEvent)

func (f ObserverFunc) Observe(event ProgressEvent) { f(event) }

type serializedObserver struct {
	observer Observer
	now      func() time.Time
	mu       sync.Mutex
}

func serial(observer Observer, now func() time.Time) *serializedObserver {
	if now == nil {
		now = time.Now
	}
	return &serializedObserver{observer: observer, now: now}
}

func (o *serializedObserver) emit(event ProgressEvent) {
	if o == nil || o.observer == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if event.At.IsZero() {
		event.At = o.now()
	}
	o.observer.Observe(event)
}
