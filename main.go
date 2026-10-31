package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}

	switch args[0] {
	case "scan":
		return runScan(args[1:])
	case "events":
		return runEvents(args[1:])
	case "ack":
		return runAck(args[1:])
	default:
		return usageError()
	}
}

func runScan(args []string) error {
	flags := flag.NewFlagSet("scan", flag.ContinueOnError)
	dbPath := flags.String("db", defaultDBPath(), "path to the local state database")
	force := flags.Bool("force", false, "re-read metadata for every MP3")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: mp3index scan [--db PATH] [--force] DIRECTORY")
	}

	store, err := OpenStore(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	summary, err := Scan(context.Background(), store, flags.Arg(0), *force, os.Stderr)
	fmt.Fprintf(os.Stderr, "scan: found %d, changed %d, unreadable %d\n", summary.Found, summary.Changed, summary.Unreadable)
	return err
}

func runEvents(args []string) error {
	flags := flag.NewFlagSet("events", flag.ContinueOnError)
	dbPath := flags.String("db", defaultDBPath(), "path to the local state database")
	limit := flags.Int("limit", 1000, "maximum number of pending events")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *limit < 1 {
		return errors.New("usage: mp3index events [--db PATH] [--limit N]")
	}

	store, err := OpenStore(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	events, err := store.PendingEvents(*limit)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			return err
		}
	}
	return nil
}

func runAck(args []string) error {
	flags := flag.NewFlagSet("ack", flag.ContinueOnError)
	dbPath := flags.String("db", defaultDBPath(), "path to the local state database")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() == 0 {
		return errors.New("usage: mp3index ack [--db PATH] SEQUENCE...")
	}

	store, err := OpenStore(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	for _, value := range flags.Args() {
		sequence, err := strconv.ParseInt(value, 10, 64)
		if err != nil || sequence < 1 {
			return fmt.Errorf("invalid event sequence %q", value)
		}
		if err := store.Ack(sequence); err != nil {
			return err
		}
	}
	return nil
}

func defaultDBPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(".", "wrir-mp3-indexer.db")
	}
	return filepath.Join(configDir, "wrir-mp3-indexer", "index.db")
}

func usageError() error {
	return errors.New("usage: mp3index <scan|events|ack> [flags]")
}
