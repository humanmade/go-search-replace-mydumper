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

### Basic Syntax

```
go-search-replace-mydumper [options] <input file|-> <output dir> <from> <to> ...
```

### File Mode

Process an existing mydumper backup file:

```bash
# Uncompressed backup
go-search-replace-mydumper backup.sql output/ old.example.com new.example.com

# Compressed backup (.gz)
go-search-replace-mydumper backup.sql.gz output/ old.example.com new.example.com

# Multiple replacements
go-search-replace-mydumper backup.sql.gz output/ \
  old1.example.com new1.example.com \
  old2.example.com new2.example.com
```

### Stream Mode (stdin)

Process mydumper output directly as it streams, without waiting for the dump to complete:

```bash
# Stream from mydumper
mydumper --stream --compress | go-search-replace-mydumper - output/ old.example.com new.example.com

# Pipe from existing file
cat backup.sql | go-search-replace-mydumper - output/ old.example.com new.example.com

# Pipe compressed file
cat backup.sql.gz | go-search-replace-mydumper - output/ old.example.com new.example.com
```

**Note:** Compression is automatically detected in stream mode by inspecting the data (gzip magic bytes), so both compressed and uncompressed streams are supported without additional flags.

### Options

- `--buffer-size`: Size of read buffer in bytes (default: 2MB)
- `--max-line-size`: Maximum allowed line size in bytes (default: 512MB)
