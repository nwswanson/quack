package eventpipe

import (
	"strconv"
	"testing"
)

func BenchmarkStorePublishRetainedFanIn(b *testing.B) {
	payload := []byte(`{"kind":"serial","chunk":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`)
	headers := map[string]string{"content-type": "application/json", "source": "bench"}

	b.Run("drop_oldest_retained", func(b *testing.B) {
		store := NewStore()
		config := Config{Name: "bench.pipe", Retain: 128}
		b.ReportAllocs()
		b.SetBytes(int64(len(payload)))
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				_, ok := store.Publish(config, Event{
					Site:       "bench",
					Topic:      "bench.pipe",
					SourceKind: "benchmark",
					SourceName: "fan-in-" + strconv.Itoa(i%16),
					Payload:    payload,
					Headers:    headers,
				})
				if !ok {
					b.Fatal("publish rejected")
				}
				i++
			}
		})
	})

	b.Run("unlimited", func(b *testing.B) {
		store := NewStore()
		config := Config{Name: "bench.pipe", Unlimited: true}
		b.ReportAllocs()
		b.SetBytes(int64(len(payload)))
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				_, ok := store.Publish(config, Event{
					Site:       "bench",
					Topic:      "bench.pipe",
					SourceKind: "benchmark",
					SourceName: "fan-in-" + strconv.Itoa(i%16),
					Payload:    payload,
					Headers:    headers,
				})
				if !ok {
					b.Fatal("publish rejected")
				}
				i++
			}
		})
	})
}

func BenchmarkStoreRecentRetained(b *testing.B) {
	store := NewStore()
	config := Config{Name: "bench.pipe", Retain: 512}
	for i := 0; i < 512; i++ {
		_, ok := store.Publish(config, Event{
			Site:    "bench",
			Topic:   "bench.pipe",
			Payload: []byte("payload-" + strconv.Itoa(i)),
			Headers: map[string]string{"seq": strconv.Itoa(i)},
		})
		if !ok {
			b.Fatal("publish rejected")
		}
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		events := store.Recent("bench", config)
		if len(events) != 512 {
			b.Fatalf("recent returned %d events, want 512", len(events))
		}
	}
}
