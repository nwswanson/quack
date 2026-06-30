package eventpipe

import (
	"strings"
	"testing"
)

func TestStorePublishRetainsNewestByDefault(t *testing.T) {
	store := NewStore()
	for i := 0; i < 3; i++ {
		if _, ok := store.Publish(Config{Name: "sensor", Retain: 2}, Event{Site: "site", Payload: []byte{byte('0' + i)}}); !ok {
			t.Fatal("publish rejected")
		}
	}
	recent := store.Recent("site", Config{Name: "sensor"})
	if len(recent) != 2 || string(recent[0].Payload) != "1" || string(recent[1].Payload) != "2" {
		t.Fatalf("recent = %#v, want newest two events", recent)
	}
}

func TestStorePublishAssignsCanonicalEnvelopeFields(t *testing.T) {
	store := NewStore()
	event, ok := store.Publish(Config{Name: "rooms", Retain: 2}, Event{
		Site: "site", Version: 17, Topic: "room.123", SourceKind: "ws", SourceName: "/chat",
		CorrelationID: "req_123", Payload: []byte(`{"type":"room.message.created","text":"hello"}`),
	})
	if !ok {
		t.Fatal("publish rejected")
	}
	if !strings.HasPrefix(event.ID, "evt_") {
		t.Fatalf("id = %q, want evt_ prefix", event.ID)
	}
	if event.Pipe != "rooms" || event.Topic != "room.123" || event.Type != "room.message.created" || event.Source != "ws:/chat" {
		t.Fatalf("event = %#v, want canonical pipe/topic/type/source", event)
	}
	if event.Time.IsZero() || event.Seq != 1 || event.Site != "site" || event.Version != 17 || event.CorrelationID != "req_123" {
		t.Fatalf("event = %#v, want host time, seq, site, version, and correlation", event)
	}
}

func TestStorePublishDropNewRejectsWhenFull(t *testing.T) {
	store := NewStore()
	config := Config{Name: "sensor", Retain: 1, Overflow: DropNew}
	if _, ok := store.Publish(config, Event{Site: "site", Payload: []byte("first")}); !ok {
		t.Fatal("first publish rejected")
	}
	if _, ok := store.Publish(config, Event{Site: "site", Payload: []byte("second")}); ok {
		t.Fatal("second publish accepted, want drop_new rejection")
	}
	recent := store.Recent("site", config)
	if len(recent) != 1 || string(recent[0].Payload) != "first" {
		t.Fatalf("recent = %#v, want first event retained", recent)
	}
}

func TestStorePublishRetainedRingKeepsChronologicalOrder(t *testing.T) {
	store := NewStore()
	config := Config{Name: "sensor", Retain: 3}
	for i := 0; i < 8; i++ {
		if _, ok := store.Publish(config, Event{Site: "site", Payload: []byte{byte('0' + i)}}); !ok {
			t.Fatal("publish rejected")
		}
	}
	recent := store.Recent("site", config)
	if got := payloads(recent); got != "567" {
		t.Fatalf("recent payloads = %q, want 567", got)
	}
}

func TestStorePublishRetainedConfigShrinkKeepsNewest(t *testing.T) {
	store := NewStore()
	for i := 0; i < 5; i++ {
		if _, ok := store.Publish(Config{Name: "sensor", Retain: 5}, Event{Site: "site", Payload: []byte{byte('0' + i)}}); !ok {
			t.Fatal("publish rejected")
		}
	}
	if _, ok := store.Publish(Config{Name: "sensor", Retain: 2}, Event{Site: "site", Payload: []byte("5")}); !ok {
		t.Fatal("publish rejected after shrink")
	}
	recent := store.Recent("site", Config{Name: "sensor"})
	if got := payloads(recent); got != "45" {
		t.Fatalf("recent payloads = %q, want 45", got)
	}
}

func TestStorePublishWildcardTopicPolicyEvictsLRU(t *testing.T) {
	store := NewStore()
	config := Config{
		Name: "room.1", Selector: "room.*", Retain: 2, KeyBy: KeyByTopic,
		MaxTopics: 2, TopicOverflow: EvictLRU,
	}
	if _, ok := store.Publish(config, Event{Site: "site", Topic: "room.1", Payload: []byte("1")}); !ok {
		t.Fatal("room.1 publish rejected")
	}
	config.Name = "room.2"
	if _, ok := store.Publish(config, Event{Site: "site", Topic: "room.2", Payload: []byte("2")}); !ok {
		t.Fatal("room.2 publish rejected")
	}
	config.Name = "room.1"
	if _, ok := store.Publish(config, Event{Site: "site", Topic: "room.1", Payload: []byte("a")}); !ok {
		t.Fatal("room.1 touch publish rejected")
	}
	config.Name = "room.3"
	if _, ok := store.Publish(config, Event{Site: "site", Topic: "room.3", Payload: []byte("3")}); !ok {
		t.Fatal("room.3 publish rejected")
	}
	if recent := store.Recent("site", Config{Name: "room.2"}); len(recent) != 0 {
		t.Fatalf("room.2 recent = %#v, want evicted", recent)
	}
	if got := payloads(store.Recent("site", Config{Name: "room.1"})); got != "1a" {
		t.Fatalf("room.1 payloads = %q, want 1a", got)
	}
	if got := payloads(store.Recent("site", Config{Name: "room.3"})); got != "3" {
		t.Fatalf("room.3 payloads = %q, want 3", got)
	}
}

func TestStorePublishWildcardTopicPolicyCanDropNewTopics(t *testing.T) {
	store := NewStore()
	config := Config{
		Name: "room.1", Selector: "room.*", KeyBy: KeyByTopic,
		MaxTopics: 1, TopicOverflow: DropNew,
	}
	if _, ok := store.Publish(config, Event{Site: "site", Topic: "room.1", Payload: []byte("1")}); !ok {
		t.Fatal("room.1 publish rejected")
	}
	config.Name = "room.2"
	if _, ok := store.Publish(config, Event{Site: "site", Topic: "room.2", Payload: []byte("2")}); ok {
		t.Fatal("room.2 publish accepted, want topic overflow rejection")
	}
	if recent := store.Recent("site", Config{Name: "room.2"}); len(recent) != 0 {
		t.Fatalf("room.2 recent = %#v, want none", recent)
	}
}

func TestStorePublishRejectsWhenSitePipeLimitExceeded(t *testing.T) {
	store := NewStore()
	limits := Limits{MaxPipes: 1}
	if _, ok := store.Publish(Config{Name: "one", SiteLimits: limits}, Event{Site: "site", Topic: "one", Payload: []byte("1")}); !ok {
		t.Fatal("first pipe publish rejected")
	}
	if _, ok := store.Publish(Config{Name: "two", SiteLimits: limits}, Event{Site: "site", Topic: "two", Payload: []byte("2")}); ok {
		t.Fatal("second pipe publish accepted, want site pipe limit rejection")
	}
}

func TestStorePublishRejectsWhenSiteTopicLimitExceededForSelectorPipe(t *testing.T) {
	store := NewStore()
	config := Config{
		Name: "chat.room.*", Selector: "chat.room.*", KeyBy: KeyBySelector, SiteLimits: Limits{MaxTopics: 1},
	}
	if _, ok := store.Publish(config, Event{Site: "site", Topic: "chat.room.1", Payload: []byte("1")}); !ok {
		t.Fatal("first topic publish rejected")
	}
	if _, ok := store.Publish(config, Event{Site: "site", Topic: "chat.room.2", Payload: []byte("2")}); ok {
		t.Fatal("second topic publish accepted, want site topic limit rejection")
	}
	if got := payloads(store.Recent("site", Config{Name: "chat.room.*"})); got != "1" {
		t.Fatalf("selector pipe payloads = %q, want only first topic retained", got)
	}
}

func TestStorePublishPrunesOldestRetainedEventsPerSite(t *testing.T) {
	store := NewStore()
	limits := Limits{MaxRetainedEvents: 2}
	for _, step := range []struct {
		pipe    string
		payload string
	}{
		{"one", "1"},
		{"two", "2"},
		{"one", "3"},
	} {
		if _, ok := store.Publish(Config{Name: step.pipe, Retain: 3, SiteLimits: limits}, Event{Site: "site", Topic: step.pipe, Payload: []byte(step.payload)}); !ok {
			t.Fatalf("%s publish rejected", step.payload)
		}
	}
	if got := payloads(store.Recent("site", Config{Name: "one"})); got != "3" {
		t.Fatalf("one payloads = %q, want oldest site event pruned", got)
	}
	if got := payloads(store.Recent("site", Config{Name: "two"})); got != "2" {
		t.Fatalf("two payloads = %q, want retained second event", got)
	}
}

func TestStorePublishPrunesOldestRetainedBytesPerSite(t *testing.T) {
	store := NewStore()
	limits := Limits{MaxRetainedBytes: 120}
	if _, ok := store.Publish(Config{Name: "one", Retain: 2, SiteLimits: limits}, Event{Site: "site", Topic: "one", Payload: []byte("large-first-payload")}); !ok {
		t.Fatal("first publish rejected")
	}
	if _, ok := store.Publish(Config{Name: "two", Retain: 2, SiteLimits: limits}, Event{Site: "site", Topic: "two", Payload: []byte("large-second-payload")}); !ok {
		t.Fatal("second publish rejected")
	}
	if got := payloads(store.Recent("site", Config{Name: "one"})); got != "" {
		t.Fatalf("one payloads = %q, want pruned by byte cap", got)
	}
	if got := payloads(store.Recent("site", Config{Name: "two"})); got != "large-second-payload" {
		t.Fatalf("two payloads = %q, want newest event retained", got)
	}
}

func payloads(events []Event) string {
	out := make([]byte, 0, len(events))
	for _, event := range events {
		out = append(out, event.Payload...)
	}
	return string(out)
}
