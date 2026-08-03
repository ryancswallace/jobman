package jobman

import (
	"reflect"
	"testing"

	"github.com/ryancswallace/jobman/internal/config"
)

func TestRedactJSONValuePreservesJSONTypes(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"api_token":          "sensitive",
		"secret_environment": map[string]any{"API_TOKEN": map[string]any{"provider": "file"}},
		"credentials":        []any{"first", "second"},
		"password_count":     float64(42),
		"secret_enabled":     true,
		"ordinary":           map[string]any{"value": "visible"},
	}
	want := map[string]any{
		"api_token":          config.Redacted,
		"secret_environment": map[string]any{},
		"credentials":        []any{},
		"password_count":     float64(0),
		"secret_enabled":     false,
		"ordinary":           map[string]any{"value": "visible"},
	}

	if got := redactJSONValue(&config.Redactor{}, input, ""); !reflect.DeepEqual(got, want) {
		t.Fatalf("redactJSONValue() = %#v, want %#v", got, want)
	}
}
