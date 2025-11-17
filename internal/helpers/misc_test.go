package helpers

import (
	"reflect"
	"testing"
)

func TestParseBool(t *testing.T) {
	trueVals := []string{"true", "TRUE", "1", "yes", "YeS"}
	for _, v := range trueVals {
		if !ParseBool(v) {
			t.Errorf("expected %q to be true", v)
		}
	}
	falseVals := []string{"false", "no", "0", "", "off"}
	for _, v := range falseVals {
		if ParseBool(v) {
			t.Errorf("expected %q to be false", v)
		}
	}
}

func TestSplitCSV(t *testing.T) {
	if out := SplitCSV(""); out != nil {
		t.Fatalf("expected nil for empty input, got %#v", out)
	}
	cases := map[string][]string{
		"a,b,c":            {"a", "b", "c"},
		" a , , b ":        {"a", "b"},
		"one, two,three ": {"one", "two", "three"},
	}
	for in, exp := range cases {
		out := SplitCSV(in)
		if !reflect.DeepEqual(out, exp) {
			t.Errorf("SplitCSV(%q) = %#v, want %#v", in, out, exp)
		}
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{
			name:   "shorter than maxLen",
			input:  "12345",
			maxLen: 10,
			want:   "12345",
		},
		{
			name:   "equal to maxLen",
			input:  "1234567890",
			maxLen: 10,
			want:   "1234567890",
		},
		{
			name:   "longer than maxLen",
			input:  "1234567890abcdef",
			maxLen: 10,
			want:   "1234567890",
		},
		{
			name:   "empty string",
			input:  "",
			maxLen: 5,
			want:   "",
		},
		{
			name:   "exactly 12 chars for docker ID",
			input:  "691110058dbc",
			maxLen: 12,
			want:   "691110058dbc",
		},
		{
			name:   "11 chars for docker ID (bug case)",
			input:  "69111005dbc",
			maxLen: 12,
			want:   "69111005dbc",
		},
		{
			name:   "truncate docker ID longer than 12",
			input:  "691110058dbc1234567890",
			maxLen: 12,
			want:   "691110058dbc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateString(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("TruncateString(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}
