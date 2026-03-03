# Go Search Replace for mydumper backups

A specialized tool for performing search and replace operations on MySQL backups created with mydumper.

## Overview

This utility extends [github.com/Automattic/go-search-replace](https://github.com/Automattic/go-search-replace) to work specifically with mydumper (stream) backup files.

## Installation

### Pre-built binaries

Download the appropriate binary for your system from the releases page.

### Building from source

To build from source, you need to have Go installed. Clone the repository and run:

```bash
make build
```

## Usage

### Split mode (default)

Reads a concatenated mydumper stream file, splits it into individual SQL files, and performs search-and-replace:

```bash
go-search-replace-mydumper <input file> <output dir> <from> <to> ...
```

### Stream mode

When mydumper is run with `--stream NO_STREAM` or `--stream NO_STREAM_AND_NO_DELETE`, it writes files to disk and outputs lightweight markers to stdout. Stream mode reads these markers from stdin, processes the already-written files in-place, and optionally forwards the marker stream to stdout for piping to myloader.

```bash
# Process files in-place only
mydumper --stream NO_STREAM -o /data | \
  go-search-replace-mydumper --stream=NO_STREAM /data from1 to1

# Process and forward to myloader
mydumper --stream NO_STREAM_AND_NO_DELETE -o /data | \
  go-search-replace-mydumper --stream=NO_STREAM_AND_NO_DELETE --forward /data from1 to1 | \
  myloader --stream
```

Flags:
- `--stream` — `NO_STREAM` or `NO_STREAM_AND_NO_DELETE`. Enables stream mode.
- `--forward` — Re-emit stdin lines to stdout after processing each file. Use when piping to myloader.
