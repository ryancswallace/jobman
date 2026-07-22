package jobman

import (
	"strconv"
	"time"

	"github.com/spf13/pflag"

	"github.com/ryancswallace/jobman/internal/config"
)

type durationFlagValue time.Duration

func newDurationFlagValue(target *time.Duration) *durationFlagValue {
	*target = 0

	return (*durationFlagValue)(target)
}

func (value *durationFlagValue) Set(encoded string) error {
	parsed, err := config.ParseDuration(encoded)
	if err != nil {
		return err
	}
	*value = durationFlagValue(parsed)

	return nil
}

func (value *durationFlagValue) String() string {
	return time.Duration(*value).String()
}

func (*durationFlagValue) Type() string {
	return "duration"
}

func durationFlag(
	flags *pflag.FlagSet,
	target *time.Duration,
	name string,
	usage string,
) {
	flags.Var(newDurationFlagValue(target), name, usage)
}

type byteSizeFlagValue uint64

func newByteSizeFlagValue(target *uint64) *byteSizeFlagValue {
	*target = 0

	return (*byteSizeFlagValue)(target)
}

func (value *byteSizeFlagValue) Set(encoded string) error {
	parsed, err := config.ParseByteSize(encoded)
	if err != nil {
		return err
	}
	*value = byteSizeFlagValue(parsed)

	return nil
}

func (value *byteSizeFlagValue) String() string {
	return strconv.FormatUint(uint64(*value), 10)
}

func (*byteSizeFlagValue) Type() string {
	return "byte-size"
}

func byteSizeFlag(flags *pflag.FlagSet, target *uint64, name, usage string) {
	flags.Var(newByteSizeFlagValue(target), name, usage)
}
