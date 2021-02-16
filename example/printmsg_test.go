package main

import "testing"

func TestMain(t *testing.T) {
	// not actual test, checking how pprof is working
	got := Run()
	if got != "" {
		t.Errorf("Error")
	}
}
