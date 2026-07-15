// Command releasemetadata renders release-specific citation metadata without
// changing the tracked source file.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var semanticVersion = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`)

type options struct {
	input   string
	output  string
	version string
	date    string
}

func main() {
	os.Exit(execute(os.Args[1:], os.Stderr))
}

func execute(arguments []string, stderr io.Writer) int {
	configuration := options{}
	flags := flag.NewFlagSet("releasemetadata", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&configuration.input, "input", "CITATION.cff", "source CFF file")
	flags.StringVar(&configuration.output, "output", "", "generated CFF file")
	flags.StringVar(&configuration.version, "version", "", "release semantic version without v")
	flags.StringVar(&configuration.date, "date", "", "release date or RFC3339 timestamp")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if err := run(configuration); err != nil {
		if _, writeErr := fmt.Fprintf(stderr, "releasemetadata: %v\n", err); writeErr != nil {
			return 2
		}

		return 1
	}

	return 0
}

func run(configuration options) error {
	if configuration.output == "" {
		return errors.New("output path is required")
	}
	if !semanticVersion.MatchString(configuration.version) {
		return fmt.Errorf("version %q is not a semantic version without a v prefix", configuration.version)
	}
	releaseDate, err := parseReleaseDate(configuration.date)
	if err != nil {
		return err
	}
	source, err := os.ReadFile(configuration.input) // #nosec G304 -- maintainer-selected release input.
	if err != nil {
		return fmt.Errorf("read citation template: %w", err)
	}
	rendered, err := render(source, configuration.version, releaseDate)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configuration.output), 0o750); err != nil {
		return fmt.Errorf("create citation output directory: %w", err)
	}
	// #nosec G306,G703 -- The maintainer selects this public build-output path.
	if err := os.WriteFile(configuration.output, rendered, 0o644); err != nil {
		return fmt.Errorf("write release citation: %w", err)
	}

	return nil
}

func parseReleaseDate(value string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC(), nil
	}
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("date %q must use YYYY-MM-DD or RFC3339: %w", value, err)
	}

	return parsed.UTC(), nil
}

func render(source []byte, version string, releaseDate time.Time) ([]byte, error) {
	lines := bytes.Split(source, []byte("\n"))
	versionFields := 0
	dateFields := 0
	for index, line := range lines {
		text := string(line)
		switch {
		case strings.HasPrefix(text, "version:"):
			lines[index] = []byte("version: " + version)
			versionFields++
		case strings.HasPrefix(text, "date-released:"):
			lines[index] = []byte("date-released: " + releaseDate.Format(time.DateOnly))
			dateFields++
		}
	}
	if versionFields != 1 || dateFields != 1 {
		return nil, fmt.Errorf(
			"citation template must contain one top-level version and date-released field; found %d and %d",
			versionFields,
			dateFields,
		)
	}

	return bytes.Join(lines, []byte("\n")), nil
}
