//go:build jobman_faultinject

package faultinject

import "os"

const (
	enableEnvironment = "JOBMAN_ENABLE_FAULT_INJECTION"
	pointEnvironment  = "JOBMAN_FAULT_POINT"
	faultExitCode     = 86
)

// Hit abruptly terminates the current assembled process at the selected test
// boundary. Both variables are required so an accidentally inherited point is
// inert.
func Hit(point string) {
	if os.Getenv(enableEnvironment) == "1" && os.Getenv(pointEnvironment) == point {
		os.Exit(faultExitCode)
	}
}
