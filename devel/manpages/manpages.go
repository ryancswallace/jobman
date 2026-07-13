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

	if err := doc.GenManTree(jobman.JobmanRootCmd, header, manPath); err != nil {
		return fmt.Errorf("generate man pages: %w", err)
	}

	return nil
}

func main() {
	if err := genManpages("."); err != nil {
		log.Fatal(err)
	}
}
