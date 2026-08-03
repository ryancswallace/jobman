package jobman

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ryancswallace/jobman/internal/config"
)

type redactorContextKey struct{}

type redactedError struct {
	underlying error
	message    string
}

func (err redactedError) Error() string { return err.message }
func (err redactedError) Unwrap() error { return err.underlying }

func configureRedactor(command *cobra.Command, configuration config.Config) error {
	redactor, err := config.NewRedactor(configuration.Redaction, nil)
	if err != nil {
		return fmt.Errorf("configure output redaction: %w", err)
	}
	command.SetContext(context.WithValue(command.Context(), redactorContextKey{}, redactor))

	return nil
}

func configureBestEffortRedactor(command *cobra.Command, options *rootOptions) {
	configuration := config.Config{}
	if loaded, err := loadConfiguration(options); err == nil {
		configuration = loaded.Config
	}
	// Validation guarantees configured patterns compile. The empty fallback is
	// always valid and still enables automatic sensitive-field-name handling.
	redactor, err := config.NewRedactor(configuration.Redaction, nil)
	if err != nil {
		redactor = &config.Redactor{}
	}
	command.SetContext(context.WithValue(command.Context(), redactorContextKey{}, redactor))
}

func commandRedactor(command *cobra.Command) *config.Redactor {
	redactor, ok := command.Context().Value(redactorContextKey{}).(*config.Redactor)
	if !ok || redactor == nil {
		redactor = &config.Redactor{}
	}

	return redactor
}

func redactCommandError(command *cobra.Command, err error) error {
	if err == nil {
		return nil
	}
	message := commandRedactor(command).RedactString(err.Error())
	if message == err.Error() {
		return err
	}

	return redactedError{underlying: err, message: message}
}

func redactField(command *cobra.Command, name, value string) string {
	return commandRedactor(command).RedactField(name, value)
}

func redactJSONValue(redactor *config.Redactor, value any, field string) any {
	if field != "" && redactor.RedactField(field, "visible") == config.Redacted {
		return redactedJSONPlaceholder(value)
	}

	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for name, child := range typed {
			result[name] = redactJSONValue(redactor, child, name)
		}

		return result
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			result[index] = redactJSONValue(redactor, child, field)
		}

		return result
	case string:
		return redactor.RedactField(field, typed)
	default:
		return value
	}
}

func redactedJSONPlaceholder(value any) any {
	switch value.(type) {
	case map[string]any:
		return map[string]any{}
	case []any:
		return []any{}
	case float64:
		return float64(0)
	case bool:
		return false
	case nil:
		return nil
	default:
		return config.Redacted
	}
}

func redactedJSON(command *cobra.Command, data any) (any, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode JSON for redaction: %w", err)
	}
	var generic any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		return nil, fmt.Errorf("decode JSON for redaction: %w", err)
	}

	return redactJSONValue(commandRedactor(command), generic, ""), nil
}
