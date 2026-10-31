package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutboxReplaysUntilAcknowledgedAndTracksDeletion(t *testing.T) {
	databasePath := t.TempDir() + "/index.db"
	store, err := OpenStore(databasePath)
	if err != nil {
		t.Fatal(err)
	}

	runID, err := store.BeginScan("/music")
	if err != nil {
		t.Fatal(err)
	}
	track := Track{ID: "/music/song.mp3", Path: "/music/song.mp3", Title: "Song"}
	changed, err := store.Upsert(runID, track, 120, 456)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("new track was not recorded as changed")
	}
	if err := store.CompleteScan(runID); err != nil {
		t.Fatal(err)
	}

	events, err := store.PendingEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Operation != "upsert" || events[0].Track.Title != "Song" {
		t.Fatalf("unexpected upsert events: %#v", events)
	}
	sequence := events[0].Sequence
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = OpenStore(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	events, err = store.PendingEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Sequence != sequence {
		t.Fatalf("pending event did not survive restart: %#v", events)
	}
	if err := store.Ack(sequence); err != nil {
		t.Fatal(err)
	}

	runID, err = store.BeginScan("/music")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteScan(runID); err != nil {
		t.Fatal(err)
	}
	events, err = store.PendingEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Operation != "delete" || events[0].ID != track.ID {
		t.Fatalf("unexpected deletion events: %#v", events)
	}
}

func TestScanRejectsFileWithoutReconcilingTracks(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "index.db")
	store, err := OpenStore(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	runID, err := store.BeginScan("/music")
	if err != nil {
		t.Fatal(err)
	}
	track := Track{ID: "/music/song.mp3", Path: "/music/song.mp3", Title: "Song"}
	if _, err := store.Upsert(runID, track, 120, 456); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteScan(runID); err != nil {
		t.Fatal(err)
	}

	filePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Scan(context.Background(), store, filePath, false, &strings.Builder{})
	if err == nil {
		t.Fatal("expected a non-directory scan target to be rejected")
	}

	events, err := store.PendingEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Operation != "upsert" {
		t.Fatalf("non-directory scan altered the outbox: %#v", events)
	}
}

func TestCompleteScanDoesNotDeleteTracksFromOtherRoots(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	firstRun, err := store.BeginScan("/music/one")
	if err != nil {
		t.Fatal(err)
	}
	track := Track{ID: "/music/one/song.mp3", Path: "/music/one/song.mp3", Title: "Song"}
	if _, err := store.Upsert(firstRun, track, 120, 456); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteScan(firstRun); err != nil {
		t.Fatal(err)
	}

	secondRun, err := store.BeginScan("/music/two")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteScan(secondRun); err != nil {
		t.Fatal(err)
	}

	events, err := store.PendingEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Operation != "upsert" || events[0].ID != track.ID {
		t.Fatalf("scan of another root altered existing tracks: %#v", events)
	}
}

func TestTrackIDIsScopedToRoot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "album", "song.mp3")
	nestedRoot := filepath.Join(root, "album")

	if trackID(root, path) == trackID(nestedRoot, path) {
		t.Fatal("overlapping roots produced the same track ID")
	}
}
