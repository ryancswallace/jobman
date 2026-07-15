package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type secretResolverFunc func(context.Context, SecretRef) (string, error)

func (function secretResolverFunc) ResolveSecret(ctx context.Context, reference SecretRef) (string, error) {
	return function(ctx, reference)
}

func TestResolveSecretsUsesOnlyNamedReferences(t *testing.T) {
	t.Parallel()

	configuration := Default()
	configuration.Secrets = map[string]SecretRef{
		"first":  {provider: "env", locator: "FIRST"},
		"second": {provider: "env", locator: "SECOND"},
	}
	called := []string{}
	resolver := secretResolverFunc(func(_ context.Context, reference SecretRef) (string, error) {
		called = append(called, reference.Locator())
		return "value-for-" + reference.Locator(), nil
	})

	resolved, err := configuration.ResolveSecrets(t.Context(), []string{"second"}, resolver)
	if err != nil {
		t.Fatalf("ResolveSecrets() error = %v", err)
	}
	if len(called) != 1 || called[0] != "SECOND" || resolved["second"] != "value-for-SECOND" {
		t.Fatalf("ResolveSecrets() = (%#v, %#v)", resolved, called)
	}
}

func TestResolveSecretsReturnsNoPartialValues(t *testing.T) {
	t.Parallel()

	configuration := Default()
	configuration.Secrets = map[string]SecretRef{
		"first":  {provider: "env", locator: "FIRST"},
		"second": {provider: "env", locator: "SECOND"},
	}
	resolver := secretResolverFunc(func(_ context.Context, reference SecretRef) (string, error) {
		if reference.Locator() == "SECOND" {
			return "", errors.New("unavailable")
		}
		return "do-not-expose", nil
	})

	resolved, err := configuration.ResolveSecrets(t.Context(), []string{"first", "second"}, resolver)
	if err == nil || resolved != nil {
		t.Fatalf("ResolveSecrets() = (%#v, %v), want nil and error", resolved, err)
	}
	if strings.Contains(err.Error(), "do-not-expose") {
		t.Fatalf("ResolveSecrets() error exposed value: %v", err)
	}
}

func TestResolveSecretsRejectsInvalidRequests(t *testing.T) {
	t.Parallel()

	configuration := Default()
	configuration.Secrets["token"] = SecretRef{provider: "env", locator: "TOKEN"}
	resolver := secretResolverFunc(func(_ context.Context, _ SecretRef) (string, error) { return "value", nil })
	if _, err := configuration.ResolveSecrets(t.Context(), []string{"missing"}, resolver); err == nil {
		t.Fatal("ResolveSecrets() accepted unknown name")
	}
	if _, err := configuration.ResolveSecrets(t.Context(), []string{"token", "token"}, resolver); err == nil {
		t.Fatal("ResolveSecrets() accepted duplicate name")
	}
	if _, err := configuration.ResolveSecrets(t.Context(), []string{"token"}, nil); err == nil {
		t.Fatal("ResolveSecrets() accepted nil resolver")
	}
}

func TestLocalSecretResolverEnvironment(t *testing.T) {
	t.Setenv("JOBMAN_TEST_SECRET", "secret-value")
	reference := SecretRef{provider: "env", locator: "JOBMAN_TEST_SECRET"}
	value, err := (LocalSecretResolver{}).ResolveSecret(t.Context(), reference)
	if err != nil {
		t.Fatalf("ResolveSecret() error = %v", err)
	}
	if value != "secret-value" {
		t.Fatalf("ResolveSecret() = %q", value)
	}
}

func TestLocalSecretResolverFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("exact\nvalue"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	reference := SecretRef{provider: "file", locator: path}
	value, err := (LocalSecretResolver{}).ResolveSecret(t.Context(), reference)
	if err != nil {
		t.Fatalf("ResolveSecret() error = %v", err)
	}
	if value != "exact\nvalue" {
		t.Fatalf("ResolveSecret() = %q", value)
	}
}

func TestLocalSecretResolverRejectsUnsafeFiles(t *testing.T) {
	if runtime.GOOS == goosWindows {
		t.Skip("Unix permission and symlink checks differ on Windows")
	}

	root := t.TempDir()
	public := filepath.Join(root, "public")
	if err := os.WriteFile(public, []byte("value"), 0o644); err != nil { //nolint:gosec // The test intentionally creates an unsafe secret file.
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := (LocalSecretResolver{}).ResolveSecret(t.Context(), SecretRef{provider: "file", locator: public}); err == nil {
		t.Fatal("ResolveSecret() accepted group/other-readable file")
	}

	private := filepath.Join(root, "private")
	if err := os.WriteFile(private, []byte("value"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(private, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	if _, err := (LocalSecretResolver{}).ResolveSecret(t.Context(), SecretRef{provider: "file", locator: link}); err == nil {
		t.Fatal("ResolveSecret() accepted symlink")
	}
}

func TestLocalSecretResolverHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (LocalSecretResolver{}).ResolveSecret(ctx, SecretRef{provider: "env", locator: "ANY"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolveSecret() error = %v, want context.Canceled", err)
	}
}

func TestLocalSecretResolverFailureEdges(t *testing.T) {
	t.Parallel()

	resolver := LocalSecretResolver{}
	if _, err := resolver.ResolveSecret(
		t.Context(), SecretRef{provider: "env", locator: "JOBMAN_DEFINITELY_MISSING_SECRET"},
	); err == nil {
		t.Fatal("ResolveSecret(missing environment) error = nil")
	}
	if _, err := resolver.ResolveSecret(
		t.Context(), SecretRef{provider: "unknown", locator: "value"},
	); err == nil {
		t.Fatal("ResolveSecret(unsupported provider) error = nil")
	}
	if _, err := resolveSecretFile(t.Context(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("resolveSecretFile(missing) error = nil")
	}
	large := filepath.Join(t.TempDir(), "large")
	if err := os.WriteFile(large, make([]byte, maxSecretBytes+1), 0o600); err != nil {
		t.Fatalf("write large secret: %v", err)
	}
	if _, err := resolveSecretFile(t.Context(), large); err == nil {
		t.Fatal("resolveSecretFile(oversize) error = nil")
	}

	configuration := Default()
	names := make([]string, maxConfiguredSecrets+1)
	if _, err := configuration.ResolveSecrets(t.Context(), names, resolver); err == nil {
		t.Fatal("ResolveSecrets(too many names) error = nil")
	}
}
