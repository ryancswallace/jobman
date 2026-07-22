// Command thirdpartynotices generates the third-party notices distributed with
// Jobman. It intentionally uses only the standard library so it cannot add a
// dependency that would need to be included in its own output.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

const defaultOutput = "THIRD_PARTY_NOTICES.md"

const (
	releaseArchitecture      = "amd64"
	releaseArchitectureARM64 = "arm64"
	releaseArchitecture386   = "386"
	releaseOSDarwin          = "darwin"
	releaseOSLinux           = "linux"
	releaseOSWindows         = "windows"
	britishLicenseName       = "LICEN" + "CE"
)

var releaseTargets = []target{
	{goos: releaseOSDarwin, goarch: releaseArchitecture},
	{goos: releaseOSDarwin, goarch: releaseArchitectureARM64},
	{goos: releaseOSLinux, goarch: releaseArchitecture386},
	{goos: releaseOSLinux, goarch: releaseArchitecture},
	{goos: releaseOSLinux, goarch: releaseArchitectureARM64},
	{goos: releaseOSWindows, goarch: releaseArchitecture386},
	{goos: releaseOSWindows, goarch: releaseArchitecture},
	{goos: releaseOSWindows, goarch: releaseArchitectureARM64},
}

type target struct {
	goos   string
	goarch string
}

type moduleJSON struct {
	Path    string      `json:"Path"`
	Version string      `json:"Version"`
	Dir     string      `json:"Dir"`
	Main    bool        `json:"Main"`
	Replace *moduleJSON `json:"Replace"`
}

type packageJSON struct {
	Module *moduleJSON `json:"Module"`
}

type dependency struct {
	path        string
	version     string
	dir         string
	replacement string
}

type noticeFile struct {
	name string
	text string
}

type standardLibraryNotice struct {
	version string
	files   []noticeFile
}

type commandRunner func(context.Context, target) ([]byte, error)

func main() {
	os.Exit(execute(context.Background(), os.Args[1:], os.Stderr, runGoList))
}

func execute(ctx context.Context, args []string, stderr io.Writer, runner commandRunner) int {
	flags := flag.NewFlagSet("thirdpartynotices", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", defaultOutput, "path to the generated notice file")
	check := flags.Bool("check", false, "verify that the output file is current")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "third-party notices: positional arguments are not supported")
		return 2
	}

	if err := run(ctx, *output, *check, runner); err != nil {
		fmt.Fprintln(stderr, "third-party notices:", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, output string, check bool, runner commandRunner) error {
	dependencies, err := discoverDependencies(ctx, releaseTargets, runner)
	if err != nil {
		return err
	}

	standardLibrary, err := loadStandardLibraryNotice(ctx)
	if err != nil {
		return err
	}
	generated, err := renderNotices(standardLibrary, dependencies)
	if err != nil {
		return err
	}
	if check {
		current, readErr := os.ReadFile(output)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", output, readErr)
		}
		if !bytes.Equal(current, generated) {
			return fmt.Errorf("%s is stale; run make gen-notices", output)
		}
		return nil
	}

	if err := writeOutput(output, generated); err != nil {
		return err
	}
	return nil
}

func runGoList(ctx context.Context, target target) ([]byte, error) {
	command := exec.CommandContext(ctx, "go", "list", "-mod=readonly", "-deps", "-json", ".")
	command.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOARCH="+target.goarch,
		"GOOS="+target.goos,
	)
	output, err := command.Output()
	if err == nil {
		return output, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil, fmt.Errorf("list %s/%s dependencies: %w: %s",
			target.goos, target.goarch, err, strings.TrimSpace(string(exitErr.Stderr)))
	}
	return nil, fmt.Errorf("list %s/%s dependencies: %w", target.goos, target.goarch, err)
}

func discoverDependencies(ctx context.Context, targets []target, runner commandRunner) ([]dependency, error) {
	byIdentity := make(map[string]dependency)
	for _, target := range targets {
		data, err := runner(ctx, target)
		if err != nil {
			return nil, err
		}
		dependencies, err := decodeDependencies(data, target)
		if err != nil {
			return nil, err
		}
		for _, dep := range dependencies {
			identity := strings.Join([]string{dep.path, dep.version, dep.replacement}, "\x00")
			byIdentity[identity] = dep
		}
	}

	dependencies := make([]dependency, 0, len(byIdentity))
	for _, dependency := range byIdentity {
		dependencies = append(dependencies, dependency)
	}
	sort.Slice(dependencies, func(left, right int) bool {
		if dependencies[left].path != dependencies[right].path {
			return dependencies[left].path < dependencies[right].path
		}
		if dependencies[left].version != dependencies[right].version {
			return dependencies[left].version < dependencies[right].version
		}
		return dependencies[left].replacement < dependencies[right].replacement
	})
	return dependencies, nil
}

func decodeDependencies(data []byte, target target) ([]dependency, error) {
	var dependencies []dependency
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var packageInfo packageJSON
		if err := decoder.Decode(&packageInfo); err != nil {
			if errors.Is(err, io.EOF) {
				return dependencies, nil
			}
			return nil, fmt.Errorf("decode go list output for %s/%s: %w",
				target.goos, target.goarch, err)
		}
		dep, include, err := dependencyFromModule(packageInfo.Module)
		if err != nil {
			return nil, err
		}
		if include {
			dependencies = append(dependencies, dep)
		}
	}
}

func dependencyFromModule(module *moduleJSON) (dependency, bool, error) {
	if module == nil || module.Main {
		return dependency{}, false, nil
	}

	dep := dependency{
		path:    module.Path,
		version: module.Version,
		dir:     module.Dir,
	}
	if module.Replace != nil {
		dep.dir = module.Replace.Dir
		dep.replacement = module.Replace.Path
		if module.Replace.Version != "" {
			dep.replacement += " " + module.Replace.Version
		}
	}
	if dep.dir == "" {
		return dependency{}, false, fmt.Errorf(
			"module %s %s has no source directory; run go mod download", dep.path, dep.version)
	}
	return dep, true, nil
}

func loadStandardLibraryNotice(ctx context.Context) (standardLibraryNotice, error) {
	command := exec.CommandContext(ctx, "go", "env", "GOROOT", "GOVERSION")
	data, err := command.Output()
	if err != nil {
		return standardLibraryNotice{}, fmt.Errorf("locate Go standard library: %w", err)
	}
	values := strings.Split(strings.TrimSpace(normalizeNewlines(string(data))), "\n")
	if len(values) != 2 || values[0] == "" || values[1] == "" {
		return standardLibraryNotice{}, errors.New("locate Go standard library: unexpected go env output")
	}

	notice := standardLibraryNotice{version: values[1]}
	for _, name := range []string{"LICENSE", "PATENTS"} {
		path := filepath.Join(values[0], name)
		data, err = os.ReadFile(path)
		if err != nil {
			return standardLibraryNotice{}, fmt.Errorf("read Go standard library %s: %w", name, err)
		}
		if !utf8.Valid(data) {
			return standardLibraryNotice{}, fmt.Errorf(
				"standard library %s for Go is not UTF-8 text", name)
		}
		notice.files = append(notice.files, noticeFile{
			name: name,
			text: normalizeNewlines(string(data)),
		})
	}
	return notice, nil
}

func renderNotices(standardLibrary standardLibraryNotice, dependencies []dependency) ([]byte, error) {
	var output strings.Builder
	output.WriteString("<!-- Code generated by go run ./devel/thirdpartynotices; DO NOT EDIT. -->\n\n")
	output.WriteString("# Third-Party Notices\n\n")
	output.WriteString("Jobman incorporates the following third-party Go modules. ")
	output.WriteString("This file contains the license, notice, and patent files found at each module root ")
	output.WriteString("for the union of the Linux, macOS, and Windows release binaries.\n")
	output.WriteString("\n## Go standard library ")
	output.WriteString(standardLibrary.version)
	output.WriteByte('\n')
	for _, file := range standardLibrary.files {
		output.WriteString("\n### ")
		output.WriteString(file.name)
		output.WriteString("\n\nSource: <https://go.dev/")
		output.WriteString(file.name)
		output.WriteString(">\n\n")
		writeIndented(&output, file.text)
	}

	for _, dependency := range dependencies {
		files, err := findNoticeFiles(dependency.dir)
		if err != nil {
			return nil, fmt.Errorf("collect notices for %s %s: %w",
				dependency.path, dependency.version, err)
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("module %s %s has no root LICENSE, COPYING, NOTICE, or PATENTS file",
				dependency.path, dependency.version)
		}

		output.WriteString("\n## ")
		output.WriteString(dependency.path)
		if dependency.version != "" {
			output.WriteByte(' ')
			output.WriteString(dependency.version)
		}
		output.WriteString("\n\nModule: <https://pkg.go.dev/")
		output.WriteString(dependency.path)
		if dependency.version != "" {
			output.WriteByte('@')
			output.WriteString(dependency.version)
		}
		output.WriteString(">\n")
		if dependency.replacement != "" {
			output.WriteString("\nReplaced by: `")
			output.WriteString(strings.ReplaceAll(dependency.replacement, "`", "``"))
			output.WriteString("`\n")
		}

		for _, file := range files {
			output.WriteString("\n### ")
			output.WriteString(file.name)
			output.WriteString("\n\n")
			writeIndented(&output, file.text)
		}
	}

	return []byte(output.String()), nil
}

func findNoticeFiles(directory string) ([]noticeFile, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read module directory: %w", err)
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !isNoticeFilename(entry.Name()) {
			continue
		}
		names = append(names, entry.Name())
	}
	slices.SortFunc(names, func(left, right string) int {
		if folded := strings.Compare(strings.ToUpper(left), strings.ToUpper(right)); folded != 0 {
			return folded
		}
		return strings.Compare(left, right)
	})

	files := make([]noticeFile, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if !utf8.Valid(data) {
			return nil, fmt.Errorf("%s is not UTF-8 text", name)
		}
		files = append(files, noticeFile{
			name: name,
			text: normalizeNewlines(string(data)),
		})
	}
	return files, nil
}

func isNoticeFilename(name string) bool {
	upper := strings.ToUpper(name)
	for _, prefix := range []string{"LICENSE", britishLicenseName, "COPYING", "NOTICE", "PATENTS"} {
		if upper == prefix || strings.HasPrefix(upper, prefix+".") ||
			strings.HasPrefix(upper, prefix+"-") || strings.HasPrefix(upper, prefix+"_") {
			return true
		}
	}
	return false
}

func normalizeNewlines(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

func writeIndented(output *strings.Builder, text string) {
	text = strings.TrimSuffix(text, "\n")
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, " \t")
		if line != "" {
			output.WriteString("    ")
			output.WriteString(line)
		}
		output.WriteByte('\n')
	}
}

func writeOutput(path string, data []byte) error {
	// The generated redistribution notice is intentionally world-readable.
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // The redistribution notice must be world-readable.
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
