// Package main generates shell completion scripts for jobman.
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/ryancswallace/jobman/jobman"
)

type completion struct {
	path     string
	generate func(io.Writer) error
}

func genAutocomplete() error {
	completions := []completion{
		{
			path:     "docs/completions/bash/jobman",
			generate: jobman.JobmanRootCmd.GenBashCompletion,
		},
		{
			path:     "docs/completions/powershell/jobman.ps1",
			generate: jobman.JobmanRootCmd.GenPowerShellCompletion,
		},
		{
			path:     "docs/completions/zsh/_jobman",
			generate: jobman.JobmanRootCmd.GenZshCompletion,
		},
	}

	for _, item := range completions {
		if err := writeCompletion(item); err != nil {
			return err
		}
	}

	return nil
}

func writeCompletion(item completion) error {
	if err := os.MkdirAll(filepath.Dir(item.path), 0o750); err != nil {
		return fmt.Errorf("create completion directory for %s: %w", item.path, err)
	}

	file, err := os.Create(item.path)
	if err != nil {
		return fmt.Errorf("create completion file %s: %w", item.path, err)
	}

	if err := item.generate(file); err != nil {
		_ = file.Close()
		return fmt.Errorf("generate completion file %s: %w", item.path, err)
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("close completion file %s: %w", item.path, err)
	}

	return nil
}

func main() {
	if err := genAutocomplete(); err != nil {
		log.Fatal(err)
	}
}
