package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dhowden/tag"
)

type Track struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Title       string `json:"title"`
	Artist      string `json:"artist,omitempty"`
	AlbumArtist string `json:"album_artist,omitempty"`
	Album       string `json:"album,omitempty"`
	Genre       string `json:"genre,omitempty"`
	Year        int    `json:"year,omitempty"`
	TrackNumber int    `json:"track_number,omitempty"`
	TrackTotal  int    `json:"track_total,omitempty"`
	DiscNumber  int    `json:"disc_number,omitempty"`
	DiscTotal   int    `json:"disc_total,omitempty"`
}

func trackID(root, path string) string {
	cleanedRoot := filepath.Clean(root)
	relativePath, err := filepath.Rel(cleanedRoot, filepath.Clean(path))
	if err != nil {
		relativePath = filepath.Clean(path)
	}
	if runtime.GOOS == "windows" {
		cleanedRoot = strings.ToLower(cleanedRoot)
		relativePath = strings.ToLower(relativePath)
	}
	rootHash := sha256.Sum256([]byte(cleanedRoot))
	return hex.EncodeToString(rootHash[:]) + ":" + filepath.ToSlash(relativePath)
}

func readTrack(root, path string) (Track, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return Track{}, err
	}
	absolutePath = filepath.Clean(absolutePath)
	file, err := os.Open(absolutePath)
	if err != nil {
		return Track{}, err
	}
	metadata, readErr := tag.ReadFrom(file)
	closeErr := file.Close()
	if readErr != nil {
		return Track{}, fmt.Errorf("read tags: %w", readErr)
	}
	if closeErr != nil {
		return Track{}, closeErr
	}

	trackNumber, trackTotal := metadata.Track()
	discNumber, discTotal := metadata.Disc()
	title := strings.TrimSpace(metadata.Title())
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(absolutePath), filepath.Ext(absolutePath))
	}

	return Track{
		ID:          trackID(root, absolutePath),
		Path:        absolutePath,
		Title:       title,
		Artist:      strings.TrimSpace(metadata.Artist()),
		AlbumArtist: strings.TrimSpace(metadata.AlbumArtist()),
		Album:       strings.TrimSpace(metadata.Album()),
		Genre:       strings.TrimSpace(metadata.Genre()),
		Year:        metadata.Year(),
		TrackNumber: trackNumber,
		TrackTotal:  trackTotal,
		DiscNumber:  discNumber,
		DiscTotal:   discTotal,
	}, nil
}
