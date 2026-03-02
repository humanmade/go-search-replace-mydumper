package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Automattic/go-search-replace/searchreplace"
)

const (
	dbName     = "testdb"
	tableName  = "testtable"
	searchURL  = "http://old.example.com/some/path"
	replaceURL = "http://new.example.com/some/path"
	lineSize   = 1024 // approximate bytes per INSERT line
)

// cacheDir returns a persistent directory for generated test data.
// Files are keyed by a hash of the generation parameters so stale data
// is never reused after parameter changes.
func cacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	return filepath.Join(home, ".cache", "go-search-replace-mydumper-bench")
}

// insertLine builds a single ~lineSize byte INSERT statement containing searchURL.
func insertLine() []byte {
	prefix := fmt.Sprintf("INSERT INTO `%s`.`%s` VALUES (1, 'some data with url %s and more text", dbName, tableName, searchURL)
	padLen := lineSize - len(prefix) - 3 // -3 for closing ');\n'
	if padLen < 0 {
		padLen = 0
	}
	return []byte(prefix + strings.Repeat("x", padLen) + "');\n")
}

// paramHash returns a short hash of the generation parameters to detect stale caches.
func paramHash(numFiles, linesPerFile int) string {
	h := sha256.New()
	fmt.Fprintf(h, "v2:numFiles=%d:linesPerFile=%d:lineSize=%d:search=%s", numFiles, linesPerFile, lineSize, searchURL)
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// ensureDataset generates (or reuses) a cached dataset with the given parameters.
// Returns: concatFile, dataDir, markers string.
func ensureDataset(numFiles, linesPerFile int) (concatFile, dataDir, markers string) {
	base := cacheDir()
	hash := paramHash(numFiles, linesPerFile)
	dir := filepath.Join(base, hash)
	sentinel := filepath.Join(dir, ".ready")

	// Check if already generated.
	if _, err := os.Stat(sentinel); err == nil {
		concatFile = filepath.Join(dir, "concat", "dump.sql")
		dataDir = filepath.Join(dir, "datadir")
		markers = readMarkers(dataDir)
		info, _ := os.Stat(concatFile)
		fmt.Fprintf(os.Stderr, "Reusing cached test data: %s (%.2f GB, %d files)\n",
			dir, float64(info.Size())/(1024*1024*1024), numFiles)
		return
	}

	// Clean any partial previous attempt.
	os.RemoveAll(dir)

	dataDir = filepath.Join(dir, "datadir")
	concatDir := filepath.Join(dir, "concat")
	os.MkdirAll(dataDir, 0755)
	os.MkdirAll(concatDir, 0755)

	concatFile = filepath.Join(concatDir, "dump.sql")

	fmt.Fprintf(os.Stderr, "Generating test data: %d files, %d lines each, ~%d GB ...\n",
		numFiles, linesPerFile, int64(numFiles)*int64(linesPerFile)*int64(lineSize)/(1024*1024*1024))

	line := insertLine()

	concatF, err := os.Create(concatFile)
	if err != nil {
		panic(err)
	}

	var markersBuf bytes.Buffer

	for i := 0; i < numFiles; i++ {
		filename := fmt.Sprintf("%s.%s.%05d.sql", dbName, tableName, i)
		marker := fmt.Sprintf("-- %s 0\n", filename)

		concatF.WriteString(marker)
		markersBuf.WriteString(marker)

		indivF, err := os.Create(filepath.Join(dataDir, filename))
		if err != nil {
			panic(err)
		}

		for j := 0; j < linesPerFile; j++ {
			concatF.Write(line)
			indivF.Write(line)
		}
		indivF.Close()
	}
	concatF.Close()

	// Write sentinel to mark complete generation.
	os.WriteFile(sentinel, []byte(hash), 0644)

	markers = markersBuf.String()

	info, _ := os.Stat(concatFile)
	fmt.Fprintf(os.Stderr, "Test data ready: %s (%.2f GB)\n",
		dir, float64(info.Size())/(1024*1024*1024))
	return
}

// ensureSingleFile generates (or reuses) a single cached SQL file with the given line count.
func ensureSingleFile(linesPerFile int) string {
	base := cacheDir()
	h := sha256.New()
	fmt.Fprintf(h, "v2:single:linesPerFile=%d:lineSize=%d:search=%s", linesPerFile, lineSize, searchURL)
	hash := hex.EncodeToString(h.Sum(nil))[:12]
	dir := filepath.Join(base, "single-"+hash)
	sentinel := filepath.Join(dir, ".ready")
	filePath := filepath.Join(dir, fmt.Sprintf("%s.%s.00000.sql", dbName, tableName))

	if _, err := os.Stat(sentinel); err == nil {
		info, _ := os.Stat(filePath)
		fmt.Fprintf(os.Stderr, "Reusing cached single file: %s (%.2f GB)\n",
			filePath, float64(info.Size())/(1024*1024*1024))
		return filePath
	}

	os.RemoveAll(dir)
	os.MkdirAll(dir, 0755)

	fmt.Fprintf(os.Stderr, "Generating single file: %d lines, ~%d GB ...\n",
		linesPerFile, int64(linesPerFile)*int64(lineSize)/(1024*1024*1024))

	line := insertLine()
	f, err := os.Create(filePath)
	if err != nil {
		panic(err)
	}
	for i := 0; i < linesPerFile; i++ {
		f.Write(line)
	}
	f.Close()

	os.WriteFile(sentinel, []byte(hash), 0644)

	info, _ := os.Stat(filePath)
	fmt.Fprintf(os.Stderr, "Single file ready: %s (%.2f GB)\n",
		filePath, float64(info.Size())/(1024*1024*1024))
	return filePath
}

// readMarkers reconstructs the markers string from a datadir's file listing.
func readMarkers(dataDir string) string {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		panic(err)
	}
	var buf bytes.Buffer
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			fmt.Fprintf(&buf, "-- %s 0\n", e.Name())
		}
	}
	return buf.String()
}

// --- Dataset parameters ---
const (
	// Multi-file dataset (~5GB): 50 files × 100K lines × ~1KB
	multiNumFiles     = 50
	multiLinesPerFile = 100_000

	// Single large file (~2GB): 1 file × ~2M lines × ~1KB
	// Increase to 16_000_000 for production-scale testing on machines with enough disk.
	singleLargeLines = 2_000_000
)

var (
	testConcatFile string
	testDataDir    string
	testMarkers    string
	testLargeFile  string
)

func TestMain(m *testing.M) {
	bufferSize = 2 * 1024 * 1024
	maxLineSize = 512 * 1024 * 1024
	testConcatFile, testDataDir, testMarkers = ensureDataset(multiNumFiles, multiLinesPerFile)
	testLargeFile = ensureSingleFile(singleLargeLines)

	os.Exit(m.Run())
}

func BenchmarkSplitMode(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		outDir, err := os.MkdirTemp("", "bench-split-out-*")
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()

		runSplitMode([]string{
			testConcatFile,
			outDir,
			searchURL,
			replaceURL,
		})

		b.StopTimer()
		os.RemoveAll(outDir)
		b.StartTimer()
	}
}

func BenchmarkStreamMode(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		scratchDir, err := os.MkdirTemp("", "bench-stream-scratch-*")
		if err != nil {
			b.Fatal(err)
		}

		entries, err := os.ReadDir(testDataDir)
		if err != nil {
			b.Fatal(err)
		}
		for _, entry := range entries {
			src := filepath.Join(testDataDir, entry.Name())
			dst := filepath.Join(scratchDir, entry.Name())
			if err := copyFile(src, dst); err != nil {
				b.Fatal(err)
			}
		}

		markers := strings.NewReader(testMarkers)
		b.StartTimer()

		runStreamMode(scratchDir, []string{searchURL, replaceURL}, markers)

		b.StopTimer()
		os.RemoveAll(scratchDir)
	}
}

func BenchmarkProcessFileInPlace(b *testing.B) {
	srcFile := filepath.Join(testDataDir, fmt.Sprintf("%s.%s.%05d.sql", dbName, tableName, 0))

	replacements := []*searchreplace.Replacement{{
		From: []byte(searchURL),
		To:   []byte(replaceURL),
	}}

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		scratchDir, err := os.MkdirTemp("", "bench-inplace-*")
		if err != nil {
			b.Fatal(err)
		}
		target := filepath.Join(scratchDir, "data.sql")
		if err := copyFile(srcFile, target); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()

		if err := processFileInPlace(target, replacements); err != nil {
			b.Fatal(err)
		}

		b.StopTimer()
		os.RemoveAll(scratchDir)
	}
}

func BenchmarkProcessLargeFileInPlace(b *testing.B) {
	replacements := []*searchreplace.Replacement{{
		From: []byte(searchURL),
		To:   []byte(replaceURL),
	}}

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		scratchDir, err := os.MkdirTemp("", "bench-inplace-large-*")
		if err != nil {
			b.Fatal(err)
		}
		target := filepath.Join(scratchDir, "large.sql")
		if err := copyFile(testLargeFile, target); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()

		if err := processFileInPlace(target, replacements); err != nil {
			b.Fatal(err)
		}

		b.StopTimer()
		os.RemoveAll(scratchDir)
	}
}

func TestStreamModeInPlace(t *testing.T) {
	const testLines = 100

	scratchDir, err := os.MkdirTemp("", "test-stream-inplace-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(scratchDir)

	insertLine := fmt.Sprintf("INSERT INTO `db`.`tbl` VALUES (1, '%s padding');\n", searchURL)

	dataFile := "db.tbl.00000.sql"
	schemaFile := "db.tbl-schema.sql"

	if err := os.WriteFile(filepath.Join(scratchDir, dataFile), bytes.Repeat([]byte(insertLine), testLines), 0644); err != nil {
		t.Fatal(err)
	}

	schemaContent := fmt.Sprintf("CREATE TABLE `tbl` COMMENT '%s';\n", searchURL)
	if err := os.WriteFile(filepath.Join(scratchDir, schemaFile), []byte(schemaContent), 0644); err != nil {
		t.Fatal(err)
	}

	markers := fmt.Sprintf("-- %s 0\n-- %s 0\n", dataFile, schemaFile)

	runStreamMode(scratchDir, []string{searchURL, replaceURL}, strings.NewReader(markers))

	dataOut, err := os.ReadFile(filepath.Join(scratchDir, dataFile))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(dataOut, []byte(searchURL)) {
		t.Errorf("data file still contains search URL %q after in-place replacement", searchURL)
	}
	if !bytes.Contains(dataOut, []byte(replaceURL)) {
		t.Errorf("data file does not contain replacement URL %q", replaceURL)
	}
	expectedLines := bytes.Count(dataOut, []byte("\n"))
	if expectedLines != testLines {
		t.Errorf("data file has %d lines, want %d", expectedLines, testLines)
	}

	schemaOut, err := os.ReadFile(filepath.Join(scratchDir, schemaFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(schemaOut) != schemaContent {
		t.Errorf("schema file was modified; got %q, want %q", string(schemaOut), schemaContent)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
