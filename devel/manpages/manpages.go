// Package jobman generates man pages for jobman.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra/doc"

	"github.com/ryancswallace/jobman/jobman"
)

func genManpages(outputRoot string) error {
	header := &doc.GenManHeader{
		Title:   "jobman",
		Section: "1",
	}
	manPath := filepath.Join(outputRoot, "docs", "manpage")
	if err := os.MkdirAll(manPath, 0o750); err != nil {
		return fmt.Errorf("create man page directory: %w", err)
	}
	if err := removeGeneratedManpages(manPath); err != nil {
		return err
	}

	command := jobman.NewCommand(jobman.Dependencies{})
	if err := doc.GenManTree(command, header, manPath); err != nil {
		return fmt.Errorf("generate man pages: %w", err)
	}

	return nil
}

func removeGeneratedManpages(manPath string) error {
	entries, err := os.ReadDir(manPath)
	if err != nil {
		return fmt.Errorf("read man page directory: %w", err)
	}

	for _, entry := range entries {
		matched, matchErr := filepath.Match("jobman*.1", entry.Name())
		if matchErr != nil {
			return fmt.Errorf("match generated man page %q: %w", entry.Name(), matchErr)
		}
		if !matched {
			continue
		}

		path := filepath.Join(manPath, entry.Name())
		if removeErr := os.Remove(path); removeErr != nil {
			return fmt.Errorf("remove generated man page %s: %w", path, removeErr)
		}
	}

	return nil
}

func main() {
	runGenerator(genManpages, log.Fatal)
}

func runGenerator(generate func(string) error, fatal func(...any)) {
	if err := generate("."); err != nil {
		fatal(err)
	}
}
