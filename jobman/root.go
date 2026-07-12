// Package jobman implements the jobman CLI.
package jobman

import (
	"fmt"

	"github.com/spf13/cobra"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"
)

var (
	cfgFile   string
	errConfig error
)

// JobmanRootCmd is root of the jobman command tree.
var JobmanRootCmd = &cobra.Command{
	Use:           "jobman [command]",
	Short:         "Run and manage background jobs without a daemon",
	Long:          "Jobman runs and manages command-line jobs with retries, timeouts, logging, and notifications without requiring a daemon.",
	SilenceErrors: true,
	SilenceUsage:  true,
	PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
		return errConfig
	},
	// The bare jobman command is an alias for jobman run.
	RunE: Run,
}

// Execute runs the root command and returns any command or configuration error.
func Execute() error {
	return JobmanRootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)
	JobmanRootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "path to a jobman configuration file")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	errConfig = nil

	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := homedir.Dir()
		if err != nil {
			errConfig = fmt.Errorf("find home directory: %w", err)

			return
		}

		viper.AddConfigPath(home)
		viper.SetConfigName(".jobman")
	}

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(JobmanRootCmd.ErrOrStderr(), "Using config file:", viper.ConfigFileUsed())
	}
}
