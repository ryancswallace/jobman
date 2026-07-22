package config

import (
	"errors"
	"fmt"
)

// ErrInvalid classifies configuration content that cannot be parsed or does
// not satisfy Jobman's schema and semantic validation rules. Filesystem and
// other operational failures deliberately do not match ErrInvalid.
var ErrInvalid = errors.New("invalid configuration")

func invalidError(err error) error {
	if err == nil || errors.Is(err, ErrInvalid) {
		return err
	}

	return fmt.Errorf("%w: %w", ErrInvalid, err)
}
