package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/Automattic/go-search-replace/searchreplace"
	"github.com/klauspost/pgzip"
)

const (
	badInputRe   = `\w:\d+:`
	inputRe      = `^[A-Za-z0-9_\-\.:/]+$`
	minInLength  = 4
	minOutLength = 2
)

var (
	input       = regexp.MustCompile(inputRe)
	bad         = regexp.MustCompile(badInputRe)
	bufferSize  int
	maxLineSize int64
	streamMode  string
	forward     bool
)

func main() {
	flag.IntVar(&bufferSize, "buffer-size", 2*1024*1024, "Size of read buffer in bytes")
	flag.Int64Var(&maxLineSize, "max-line-size", 512*1024*1024, "Maximum allowed line size in bytes")
	flag.StringVar(&streamMode, "stream", "", "Stream mode: NO_STREAM or NO_STREAM_AND_NO_DELETE (read mydumper markers from stdin, process files in-place)")
	flag.BoolVar(&forward, "forward", false, "Forward stdin lines to stdout (for piping to myloader, requires --stream)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  Split mode:  %s [options] <input file> <output dir> <from> <to> ...\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  Stream mode: %s --stream=NO_STREAM_AND_NO_DELETE [--forward] [options] <datadir> <from> <to> ...\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	args := flag.Args()

	if streamMode != "" {
		if streamMode != "NO_STREAM" && streamMode != "NO_STREAM_AND_NO_DELETE" {
			fmt.Fprintf(os.Stderr, "Invalid --stream value %q: must be NO_STREAM or NO_STREAM_AND_NO_DELETE\n", streamMode)
			os.Exit(1)
			return
		}
		if len(args) < 1 {
			flag.Usage()
			os.Exit(1)
			return
		}
		dataDir := args[0]
		if info, err := os.Stat(dataDir); err != nil || !info.IsDir() {
			fmt.Fprintf(os.Stderr, "Data directory %s does not exist or is not a directory\n", dataDir)
			os.Exit(1)
			return
		}
		runStreamMode(dataDir, args[1:], os.Stdin)
	} else {
		if len(args) < 2 {
			flag.Usage()
			os.Exit(1)
			return
		}
		runSplitMode(args)
	}
}

func runSplitMode(args []string) {
	inputFilePath := args[0]

	if _, err := os.Stat(inputFilePath); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "File %s does not exist\n", inputFilePath)
		os.Exit(1)
		return
	}

	inputFile, err := os.Open(inputFilePath)
	if err != nil {
		panic(err)
	}
	defer inputFile.Close()

	// Create a reader based on file extension
	var reader io.Reader
	if filepath.Ext(inputFilePath) == ".gz" {
		gzr, err := pgzip.NewReader(inputFile)
		if err != nil {
			panic(err)
		}
		defer gzr.Close()
		reader = gzr
	} else {
		reader = inputFile
	}

	dataFileRegex := regexp.MustCompile(`\d+.sql$`)

	outputDir := args[1]

	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			fmt.Fprintln(os.Stderr, fmt.Sprintf("Error creating output directory: %v", err))
			os.Exit(1)
			return
		}
	}

	// Remove the first two arguments and leave only the replacements
	rawReplacements := args[2:]

	var replacements []*searchreplace.Replacement

	if len(rawReplacements)%2 > 0 {
		fmt.Fprintln(os.Stderr, "All replacements must have a <from> and <to> value")
		os.Exit(1)
		return
	}

	fmt.Println("go-search-replace-mydumper: Processing file:", inputFilePath)
	fmt.Println("go-search-replace-mydumper: Output directory:", outputDir)
	fmt.Println("go-search-replace-mydumper: Replacements:", rawReplacements)

	start := time.Now()

	var from, to string
	for i := 0; i < len(rawReplacements)/2; i++ {
		from = rawReplacements[i*2]
		if !validInput(from, minInLength) {
			fmt.Fprintln(os.Stderr, "Invalid <from> URL, minimum length is 4")
			os.Exit(2)
			return
		}

		to = rawReplacements[(i*2)+1]
		if !validInput(to, minOutLength) {
			fmt.Fprintln(os.Stderr, "Invalid <to>, minimum length is 2")
			os.Exit(3)
			return
		}

		replacements = append(replacements, &searchreplace.Replacement{
			From: []byte(from),
			To:   []byte(to),
		})
	}

	hasReplacements := len(replacements) > 0

	pattern := `^--\s+([\S]+)\s+\d+`
	filenameRegex := regexp.MustCompile(pattern)

	keep := true
	var newFilename string

	var output *os.File
	var writer *bufio.Writer

	fileLinePrefix := []byte("-- ")

	isDataFile := false

	r := bufio.NewReaderSize(reader, bufferSize)

	for {
		line, err := readFullLine(r)

		if err != nil {
			if err == io.EOF {
				if 0 == len(line) {
					break
				}
			} else {
				fmt.Fprintln(os.Stderr, err.Error())

				os.Exit(1)
			}
		}

		if bytes.HasPrefix(line, fileLinePrefix) {
			if matches := filenameRegex.FindSubmatch(line); matches != nil {
				if output != nil {
					if err = writer.Flush(); err != nil {
						fmt.Printf("Error flushing buffer: %v\n", err)
						os.Exit(1)
						return
					}
					if err = output.Close(); err != nil {
						fmt.Printf("Error closing file: %v\n", err)
						os.Exit(1)
						return
					}
				}

				newFilename = string(matches[1])
				isDataFile = dataFileRegex.MatchString(newFilename)

				outputFile := filepath.Join(outputDir, newFilename)

				output, err = os.OpenFile(outputFile, os.O_CREATE|os.O_WRONLY, 0644)
				if err != nil {
					fmt.Printf("Error opening file: %v\n", err)
					return
				}
				writer = bufio.NewWriterSize(output, bufferSize)

				keep = true
			}
		} else {
			if keep && writer != nil {
				if isDataFile {
					if hasReplacements && lineContainsAny(line, replacements) {
						replaced := searchreplace.FixLine(&line, replacements)
						_, err = writer.Write(*replaced)
					} else {
						_, err = writer.Write(line)
					}
					if err != nil {
						fmt.Printf("Error writing to buffer: %v\n", err)
						return
					}
				} else {
					_, err = writer.Write(line)
					if err != nil {
						fmt.Printf("Error writing to buffer: %v\n", err)
						return
					}
				}
			}
		}
	}

	if writer != nil {
		if err = writer.Flush(); err != nil {
			fmt.Printf("Error flushing buffer: %v\n", err)
			os.Exit(1)
			return
		}
	}

	if output != nil {
		if err = output.Close(); err != nil {
			fmt.Printf("Error closing file: %v\n", err)
			os.Exit(1)
			return
		}
	}

	fmt.Printf("go-search-replace-mydumper: Finished successfully. took %v\n", time.Since(start))
}

func runStreamMode(dataDir string, rawReplacements []string, stdinReader io.Reader) {
	if len(rawReplacements)%2 > 0 {
		fmt.Fprintln(os.Stderr, "All replacements must have a <from> and <to> value")
		os.Exit(1)
		return
	}

	var replacements []*searchreplace.Replacement
	for i := 0; i < len(rawReplacements)/2; i++ {
		from := rawReplacements[i*2]
		if !validInput(from, minInLength) {
			fmt.Fprintln(os.Stderr, "Invalid <from> URL, minimum length is 4")
			os.Exit(2)
			return
		}
		to := rawReplacements[(i*2)+1]
		if !validInput(to, minOutLength) {
			fmt.Fprintln(os.Stderr, "Invalid <to>, minimum length is 2")
			os.Exit(3)
			return
		}
		replacements = append(replacements, &searchreplace.Replacement{
			From: []byte(from),
			To:   []byte(to),
		})
	}

	hasReplacements := len(replacements) > 0

	dataFileRegex := regexp.MustCompile(`\d+\.sql$`)
	markerRegex := regexp.MustCompile(`^--\s+([\S]+)\s+\d+`)

	fmt.Fprintln(os.Stderr, "go-search-replace-mydumper: Stream mode, datadir:", dataDir)
	fmt.Fprintln(os.Stderr, "go-search-replace-mydumper: Replacements:", rawReplacements)

	start := time.Now()
	filesProcessed := 0

	stdout := bufio.NewWriter(os.Stdout)
	defer stdout.Flush()

	scanner := bufio.NewScanner(stdinReader)
	scanner.Buffer(make([]byte, 0, bufferSize), bufferSize)

	for scanner.Scan() {
		line := scanner.Text()

		if matches := markerRegex.FindStringSubmatch(line); matches != nil {
			filename := matches[1]

			if hasReplacements && dataFileRegex.MatchString(filename) {
				filePath := filepath.Join(dataDir, filename)
				if err := processFileInPlace(filePath, replacements); err != nil {
					fmt.Fprintf(os.Stderr, "go-search-replace-mydumper: Error processing %s: %v\n", filename, err)
					os.Exit(1)
					return
				}
				filesProcessed++
			}
		}

		if forward {
			fmt.Fprintln(stdout, line)
			stdout.Flush()
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "go-search-replace-mydumper: Error reading stdin: %v\n", err)
		os.Exit(1)
		return
	}

	fmt.Fprintf(os.Stderr, "go-search-replace-mydumper: Stream finished. Processed %d data files in %v\n", filesProcessed, time.Since(start))
}

func processFileInPlace(filePath string, replacements []*searchreplace.Replacement) error {
	src, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	tmpPath := filePath + ".tmp"
	dst, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	writer := bufio.NewWriterSize(dst, bufferSize)
	r := bufio.NewReaderSize(src, bufferSize)

	for {
		line, err := readFullLine(r)
		if err != nil {
			if err == io.EOF {
				if len(line) == 0 {
					break
				}
			} else {
				dst.Close()
				os.Remove(tmpPath)
				return fmt.Errorf("read line: %w", err)
			}
		}

		if lineContainsAny(line, replacements) {
			replaced := searchreplace.FixLine(&line, replacements)
			if _, werr := writer.Write(*replaced); werr != nil {
				dst.Close()
				os.Remove(tmpPath)
				return fmt.Errorf("write: %w", werr)
			}
		} else {
			if _, werr := writer.Write(line); werr != nil {
				dst.Close()
				os.Remove(tmpPath)
				return fmt.Errorf("write: %w", werr)
			}
		}
	}

	if err := writer.Flush(); err != nil {
		dst.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("flush: %w", err)
	}

	if err := dst.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}

	src.Close()

	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}

	return nil
}

// lineContainsAny checks if line contains any of the replacement "from" byte sequences.
func lineContainsAny(line []byte, replacements []*searchreplace.Replacement) bool {
	for _, r := range replacements {
		if bytes.Contains(line, r.From) {
			return true
		}
	}
	return false
}

// readFullLine reads a complete line from the reader, handling lines larger than the buffer size
// by joining fragments until the complete line is read
func readFullLine(r *bufio.Reader) ([]byte, error) {
	var lineBuffer bytes.Buffer
	var currentSize int64

	for {
		fragment, isPrefix, err := r.ReadLine()

		if err != nil {
			return lineBuffer.Bytes(), err
		}

		currentSize += int64(len(fragment))
		if currentSize > maxLineSize {
			return nil, fmt.Errorf("line exceeds maximum size of %d MB", maxLineSize/(1024*1024))
		}

		lineBuffer.Write(fragment)

		if !isPrefix {
			lineBuffer.Write([]byte{'\n'})

			return lineBuffer.Bytes(), nil
		}
	}
}

func fromEntriesContainsRegex(replacements []*searchreplace.Replacement) *regexp.Regexp {
	allFromStartWithSlash := true

	fromEntries := make([]string, len(replacements))
	for i, replacement := range replacements {
		fromEntries[i] = string(replacement.From)
		if !bytes.HasPrefix(replacement.To, []byte("//")) {
			allFromStartWithSlash = false
		}
	}

	fromEntriesContainsPattern := ""

	// If all replacements start with a double-slash (which is a common scenario), we
	// can optimize the regex by adding the double-slash at the beginning of the regex.
	// Sort of Aho–Corasick algorithm.
	if allFromStartWithSlash {
		fromEntriesContainsPattern += regexp.QuoteMeta("//")
	}

	fromEntriesContainsPattern += "(?:"
	for i, from := range fromEntries {
		if i > 0 {
			fromEntriesContainsPattern += "|"
		}

		if allFromStartWithSlash {
			from = from[2:]
		}

		fromEntriesContainsPattern += regexp.QuoteMeta(from)
	}
	fromEntriesContainsPattern += ")"

	return regexp.MustCompile(fromEntriesContainsPattern)
}

func validInput(in string, length int) bool {
	if len(in) < length {
		return false
	}

	if !input.MatchString(in) {
		return false
	}

	if bad.MatchString(in) {
		return false
	}

	return true
}
