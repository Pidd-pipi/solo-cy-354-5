package util

import (
	"regexp"
	"testing"
)

func TestGenerateHandoverCode(t *testing.T) {
	seen := map[string]bool{}
	var code string
	var err error
	for i := 0; i < 200; i++ {
		code, err = GenerateHandoverCode()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(code) != 6 {
			t.Fatalf("expected 6-digit code, got %q", code)
		}
		if !regexp.MustCompile(`^\d{6}$`).MatchString(code) {
			t.Fatalf("code must be 6 digits, got %q", code)
		}
		seen[code] = true
	}
	if len(seen) < 150 {
		t.Fatalf("codes look insufficiently random: only %d unique in 200 draws", len(seen))
	}
}

func TestFormatHandoverCode(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "000000"},
		{7, "000007"},
		{42817, "042817"},
		{999999, "999999"},
	}
	for _, tt := range tests {
		if got := FormatHandoverCode(tt.in); got != tt.want {
			t.Fatalf("FormatHandoverCode(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
