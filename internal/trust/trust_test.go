package trust

import (
	"testing"
	"time"
)

func TestStoreOperations(t *testing.T) {
	store := New()
	if store.Has("one") {
		t.Fatal("new store contains a record")
	}
	store.Add(Record{Fingerprint: "two", Hostname: "two"})
	store.Add(Record{Fingerprint: "one", Hostname: "one"})
	if !store.Has("one") || !store.Has("two") {
		t.Fatal("records were not added")
	}
	record, ok := store.Get("one")
	if !ok || record.Hostname != "one" {
		t.Fatalf("unexpected record: %+v", record)
	}
	records := store.List()
	if len(records) != 2 || records[0].Fingerprint != "one" || records[1].Fingerprint != "two" {
		t.Fatalf("unexpected order: %+v", records)
	}
	store.Remove("one")
	if store.Has("one") || !store.Has("two") {
		t.Fatal("remove did not remove")
	}
	snapshot := store.Snapshot()
	if len(snapshot) != 1 || snapshot["two"].Fingerprint != "two" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	store.Replace(map[string]Record{"three": {Fingerprint: "three"}})
	if store.Has("two") || !store.Has("three") {
		t.Fatal("replace did not replace")
	}
}

func TestAddSetsPairedAt(t *testing.T) {
	store := New()
	store.Add(Record{Fingerprint: "one"})
	record, _ := store.Get("one")
	if record.PairedAt.IsZero() {
		t.Fatal("PairedAt was not set")
	}
	if time.Since(record.PairedAt) > time.Minute {
		t.Fatalf("PairedAt is not recent: %v", record.PairedAt)
	}
}
