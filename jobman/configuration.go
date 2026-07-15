package jobman

import (
	"fmt"
	"os"

	"github.com/ryancswallace/jobman/internal/config"
)

func loadConfiguration(options *rootOptions) (config.Loaded, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return config.Loaded{}, fmt.Errorf("resolve configuration working directory: %w", err)
	}
	sources, err := config.DiscoverSources(config.DiscoveryOptions{
		ExplicitPath:   options.configPath,
		ProjectStart:   workingDirectory,
		Environment:    os.Environ(),
		UseEnvironment: true,
	})
	if err != nil {
		return config.Loaded{}, fmt.Errorf("discover configuration: %w", err)
	}
	loaded, err := config.Load(sources...)
	if err != nil {
		return config.Loaded{}, fmt.Errorf("load configuration: %w", err)
	}

	return loaded, nil
}
