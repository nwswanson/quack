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
