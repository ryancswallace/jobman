//go:build !jobman_faultinject

// Package faultinject provides inert production hooks and opt-in assembled-
// binary crash points used to verify durable process boundaries.
package faultinject

// Hit is a production no-op.
func Hit(string) {}
