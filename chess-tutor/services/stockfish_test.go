package services

import (
	"testing"
)

func TestClassifyMove(t *testing.T) {
	tests := []struct {
		name         string
		evalDiff     float64
		isBlunder    bool
		want         string
	}{
		{"large negative diff blunder", -3.5, false, "blunder"},
		{"large positive diff blunder", 3.5, false, "blunder"},
		{"mistake level negative", -2.0, false, "mistake"},
		{"mistake level positive", 2.0, false, "mistake"},
		{"inaccuracy level negative", -1.0, false, "inaccuracy"},
		{"inaccuracy level positive", 1.0, false, "inaccuracy"},
		{"good move negative", -0.3, false, "good"},
		{"good move positive", 0.3, false, "good"},
		{"excellent move no diff", 0, false, "excellent"},
		{"excellent move tiny diff", 0.1, false, "excellent"},
		{"isBlunder overrides small diff", 0.1, true, "blunder"},
		{"isBlunder overrides any diff", -0.5, true, "blunder"},
		{"boundary blunder", 3.0, false, "blunder"},
		{"boundary mistake max", 2.99, false, "mistake"},
		{"boundary mistake min", 1.5, false, "mistake"},
		{"boundary inaccuracy max", 1.49, false, "inaccuracy"},
		{"boundary inaccuracy min", 0.5, false, "inaccuracy"},
		{"boundary good max", 0.49, false, "good"},
		{"boundary good min", 0.2, false, "good"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyMove(tt.evalDiff, 0, tt.isBlunder)
			if got != tt.want {
				t.Errorf("ClassifyMove(%v, 0, %v) = %q, want %q", tt.evalDiff, tt.isBlunder, got, tt.want)
			}
		})
	}
}
