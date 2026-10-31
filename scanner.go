package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type ScanSummary struct {
	Found      int
	Changed    int
	Unreadable int
}

func Scan(ctx context.Context, store *Store, root string, force bool, warnings io.Writer) (ScanSummary, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return ScanSummary{}, err
	}
	absoluteRoot, err = filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return ScanSummary{}, err
	}
	if err := scanRootExists(absoluteRoot); err != nil {
		return ScanSummary{}, err
	}
	runID, err := store.BeginScan(filepath.Clean(absoluteRoot))
	if err != nil {
		return ScanSummary{}, err
	}

	var summary ScanSummary
	var walkErrors []error
	err = filepath.WalkDir(absoluteRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if walkErr != nil {
			walkErrors = append(walkErrors, walkErr)
			return nil
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".mp3") {
			return nil
		}
		summary.Found++
		info, err := entry.Info()
		if err != nil {
			walkErrors = append(walkErrors, fmt.Errorf("stat %s: %w", path, err))
			return nil
		}

		id := trackID(absoluteRoot, path)
		unchanged, err := store.Unchanged(id, info.Size(), info.ModTime().UnixNano(), force)
		if err != nil {
			return err
		}
		if unchanged {
			return store.MarkSeen(runID, id)
		}

		track, err := readTrack(absoluteRoot, path)
		if err != nil {
			summary.Unreadable++
			fmt.Fprintf(warnings, "warning: %s: %v\n", path, err)
			return store.MarkSeen(runID, id)
		}
		changed, err := store.Upsert(runID, track, info.Size(), info.ModTime().UnixNano())
		if changed {
			summary.Changed++
		}
		return err
	})
	if err != nil {
		return summary, err
	}
	if len(walkErrors) > 0 {
		return summary, fmt.Errorf("scan incomplete; skipped deletion reconciliation: %w", errors.Join(walkErrors...))
	}
	if err := store.CompleteScan(runID); err != nil {
		return summary, err
	}
	return summary, nil
}

func scanRootExists(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", root)
	}
	return nil
}
