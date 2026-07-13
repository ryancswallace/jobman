package jobman

import (
	"bytes"
	"testing"

	"github.com/spf13/viper"
)

func TestConfig(_ *testing.T) {
}

// FuzzConfigYAML checks that arbitrary user-supplied configuration cannot
// crash the YAML parser used by jobman.
func FuzzConfigYAML(f *testing.F) {
	f.Add([]byte("timeout: 30s\nretries: 3\n"))
	f.Add([]byte("notifications:\n  enabled: true\n"))
	f.Add([]byte("{}\n"))
	f.Add([]byte("not: [valid\n"))

	f.Fuzz(func(_ *testing.T, data []byte) {
		config := viper.New()
		config.SetConfigType("yaml")

		// Invalid input is expected; only a panic indicates a fuzz failure.
		if err := config.ReadConfig(bytes.NewReader(data)); err != nil {
			return
		}
	})
}
