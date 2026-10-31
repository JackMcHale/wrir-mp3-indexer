# wrir-mp3-indexer

A local Go scanner that walks a music directory, extracts MP3 tags, and keeps a restartable stream of search-index updates in SQLite. SQLite is used only for the file manifest and durable outbox; search is handled by a downstream consumer.

## Build

```powershell
go build -o mp3index.exe .
```

## Scan and consume updates

```powershell
mp3index.exe scan "D:\Music"
mp3index.exe events
mp3index.exe ack 1
```

`scan` emits an `upsert` event when a track's indexed metadata changes and a `delete` event after a complete scan no longer finds a previously indexed file. A scan that encounters directory traversal errors does not reconcile deletions. Unreadable MP3s are reported and skipped.

`events` prints pending events as newline-delimited JSON. Each event has a monotonically increasing sequence, an operation, a stable document ID, and (for upserts) the track document. The consumer should acknowledge a sequence with `ack` only after the search engine accepts it. Unacknowledged events are replayed after restarts, so consumers should apply upserts and deletes idempotently and in sequence order.

The state database defaults to the current user's configuration directory under `wrir-mp3-indexer/index.db`. One database can track multiple roots; deletion reconciliation is scoped to the root being scanned. Use `--db PATH` on each command to select another database. Use `scan --force DIRECTORY` to re-read tags even when file size and modification time are unchanged.