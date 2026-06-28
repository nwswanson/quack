package eventpipe

import "testing"

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

func payloads(events []Event) string {
	out := make([]byte, 0, len(events))
	for _, event := range events {
		out = append(out, event.Payload...)
	}
	return string(out)
}
