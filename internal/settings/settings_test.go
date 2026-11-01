package settings

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestMigrate(t *testing.T) {
	s, err := Migrate(nil)
	if err != nil || !reflect.DeepEqual(s, Defaults()) {
		t.Fatalf("empty: %+v %v", s, err)
	}
	// An older/partial document keeps what it has and gains defaults.
	s, err = Migrate([]byte(`{"version":1,"general":{"language":"zh-Hans"},"studio":{"autoplay":false},"unknown":1}`))
	if err != nil || s.General.Language != "zh-Hans" || s.Studio.Autoplay || s.Appearance.Scale != 1 || s.Appearance.Theme != "system" {
		t.Fatalf("partial: %+v %v", s, err)
	}
	data, _ := json.Marshal(s)
	again, err := Migrate(data)
	if err != nil || !reflect.DeepEqual(again, s) {
		t.Fatalf("round trip: %+v %v", again, err)
	}
	if _, err := Migrate([]byte(`{`)); err == nil {
		t.Error("bad JSON accepted")
	}
}

func TestStore(t *testing.T) {
	ctx := context.Background()
	docs := &memDocs{}
	var events []Changed
	s, err := Open(ctx, docs, func(c Changed) { events = append(events, c) })
	if err != nil || !reflect.DeepEqual(s.Get(), Defaults()) {
		t.Fatalf("fresh: %+v %v", s.Get(), err)
	}
	if err := s.Update(ctx, func(s *Settings) {
		s.General.Language = "zh-Hans"
		s.Appearance.Scale = 1.12
		s.Appearance.Theme = "sepia"
		s.Models.Endpoints = []string{" https://hf-mirror.com/ ", "https://hf-mirror.com", ""}
	}); err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	if got.General.Language != "zh-Hans" || got.Appearance.Scale != 1.1 || got.Appearance.Theme != "system" ||
		!reflect.DeepEqual(got.Models.Endpoints, []string{"https://hf-mirror.com"}) {
		t.Fatalf("normalized: %+v", got)
	}
	if len(events) != 1 || events[0].Old.General.Language != "" || events[0].New.General.Language != "zh-Hans" {
		t.Fatalf("events %+v", events)
	}
	// A copy: changing it changes nothing.
	got.Models.Endpoints[0] = "x"
	if s.Get().Models.Endpoints[0] != "https://hf-mirror.com" {
		t.Error("Get returned shared state")
	}
	// No change: nothing published.
	s.Update(ctx, func(s *Settings) { s.General.Language = "zh-Hans" })
	if len(events) != 1 {
		t.Errorf("no-op published: %d", len(events))
	}
	// Reopened from disk.
	again, err := Open(ctx, docs, nil)
	if err != nil || !reflect.DeepEqual(again.Get(), s.Get()) {
		t.Fatalf("reopened %+v %v", again.Get(), err)
	}
	if _, ok := docs.m[Key]; !ok {
		t.Error("not saved")
	}
	// A broken document: defaults, and an error to log.
	docs.m[Key] = []byte("{")
	broken, err := Open(ctx, docs, nil)
	if err == nil || !reflect.DeepEqual(broken.Get(), Defaults()) {
		t.Errorf("broken: %+v %v", broken.Get(), err)
	}
}

func TestMemory(t *testing.T) {
	s := NewMemory()
	s.Update(context.Background(), func(s *Settings) { s.Studio.Autoplay = false })
	if s.Get().Studio.Autoplay {
		t.Error("not updated")
	}
}
