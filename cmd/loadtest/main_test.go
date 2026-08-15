package main

import "testing"

func TestDefaultConcurrency(t *testing.T) {
	tests := map[string]int{
		"topup":     postedConcurrency,
		"withdraw":  postedConcurrency,
		"authorize": riskConcurrency,
		"increment": riskConcurrency,
	}
	for operation, expected := range tests {
		t.Run(operation, func(t *testing.T) {
			if actual := defaultConcurrency(operation); actual != expected {
				t.Fatalf("defaultConcurrency(%q) = %d, want %d", operation, actual, expected)
			}
		})
	}
}
