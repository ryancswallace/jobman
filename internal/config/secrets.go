package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"
)

const maxSecretBytes = 64 * 1024

// SecretResolver resolves a reference directly into supervisor memory.
type SecretResolver interface {
	ResolveSecret(context.Context, SecretRef) (string, error)
}

// LocalSecretResolver resolves the built-in env and file providers. Secret
// files must be regular, non-symlink files and private to the current user on
// platforms with Unix permission bits.
type LocalSecretResolver struct{}

// ResolveSecret implements SecretResolver without logging or persisting values.
func (LocalSecretResolver) ResolveSecret(ctx context.Context, reference SecretRef) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("resolve secret: %w", err)
	}
	switch reference.Provider() {
	case "env":
		value, found := os.LookupEnv(reference.Locator())
		if !found {
			return "", errors.New("resolve environment secret: variable is not set")
		}

		return value, nil
	case fileKind:
		return resolveSecretFile(ctx, reference.Locator())
	default:
		return "", fmt.Errorf("resolve secret: unsupported provider %q", reference.Provider())
	}
}

// ResolveSecrets resolves only requested registry names. On any failure it
// returns no partial value map and never includes a resolved value in errors.
func (configuration Config) ResolveSecrets(
	ctx context.Context,
	names []string,
	resolver SecretResolver,
) (map[string]string, error) {
	if resolver == nil {
		return nil, errors.New("resolve secrets: resolver is nil")
	}
	if len(names) > maxConfiguredSecrets {
		return nil, fmt.Errorf("resolve secrets: at most %d names are allowed", maxConfiguredSecrets)
	}
	resolved := make(map[string]string, len(names))
	for _, name := range names {
		if _, duplicate := resolved[name]; duplicate {
			return nil, fmt.Errorf("resolve secrets: duplicate name %q", name)
		}
		reference, found := configuration.Secrets[name]
		if !found {
			return nil, fmt.Errorf("resolve secrets: unknown secret %q", name)
		}
		value, err := resolver.ResolveSecret(ctx, reference)
		if err != nil {
			return nil, fmt.Errorf("resolve secret %q with provider %q: %w", name, reference.Provider(), err)
		}
		resolved[name] = value
	}

	return resolved, nil
}

func resolveSecretFile(ctx context.Context, path string) (value string, returnedErr error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect secret file: %w", err)
	}
	if validationErr := validateSecretFileInfo(info); validationErr != nil {
		return "", validationErr
	}

	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open secret file: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && returnedErr == nil {
			value = ""
			returnedErr = fmt.Errorf("close secret file: %w", closeErr)
		}
	}()
	openedInfo, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect opened secret file: %w", err)
	}
	if !os.SameFile(info, openedInfo) {
		return "", errors.New("inspect opened secret file: path changed while opening")
	}
	if validationErr := validateSecretFileInfo(openedInfo); validationErr != nil {
		return "", validationErr
	}

	data, err := io.ReadAll(io.LimitReader(file, maxSecretBytes+1))
	if err != nil {
		return "", fmt.Errorf("read secret file: %w", err)
	}
	if len(data) > maxSecretBytes {
		return "", fmt.Errorf("read secret file: value exceeds %d bytes", maxSecretBytes)
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("resolve secret: %w", err)
	}

	return string(data), nil
}

func validateSecretFileInfo(info fs.FileInfo) error {
	if !info.Mode().IsRegular() {
		return errors.New("inspect secret file: path must be a regular non-symlink file")
	}
	if runtime.GOOS != goosWindows && info.Mode().Perm()&0o077 != 0 {
		return errors.New("inspect secret file: permissions must not grant group or other access")
	}

	return nil
}
