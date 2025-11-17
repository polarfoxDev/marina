package helpers

import (
	"os"
	"path/filepath"
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

func TestFilterNonEmptyPaths(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()
	
	// Create test files
	emptyFile := filepath.Join(tmpDir, "empty.txt")
	if err := os.WriteFile(emptyFile, []byte{}, 0644); err != nil {
		t.Fatalf("failed to create empty file: %v", err)
	}
	
	nonEmptyFile := filepath.Join(tmpDir, "nonempty.txt")
	if err := os.WriteFile(nonEmptyFile, []byte("content"), 0644); err != nil {
		t.Fatalf("failed to create non-empty file: %v", err)
	}
	
	testDir := filepath.Join(tmpDir, "testdir")
	if err := os.Mkdir(testDir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}
	
	nonExistentFile := filepath.Join(tmpDir, "nonexistent.txt")
	
	tests := []struct {
		name           string
		paths          []string
		wantFiltered   []string
		wantRemovedLen int
	}{
		{
			name:           "empty list",
			paths:          []string{},
			wantFiltered:   []string{},
			wantRemovedLen: 0,
		},
		{
			name:           "only non-empty file",
			paths:          []string{nonEmptyFile},
			wantFiltered:   []string{nonEmptyFile},
			wantRemovedLen: 0,
		},
		{
			name:           "only empty file",
			paths:          []string{emptyFile},
			wantFiltered:   []string{},
			wantRemovedLen: 1,
		},
		{
			name:           "mix of empty and non-empty",
			paths:          []string{nonEmptyFile, emptyFile},
			wantFiltered:   []string{nonEmptyFile},
			wantRemovedLen: 1,
		},
		{
			name:           "directory is included",
			paths:          []string{testDir},
			wantFiltered:   []string{testDir},
			wantRemovedLen: 0,
		},
		{
			name:           "non-existent file is removed",
			paths:          []string{nonExistentFile},
			wantFiltered:   []string{},
			wantRemovedLen: 1,
		},
		{
			name:           "complex mix",
			paths:          []string{nonEmptyFile, emptyFile, testDir, nonExistentFile},
			wantFiltered:   []string{nonEmptyFile, testDir},
			wantRemovedLen: 2,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered, removed := FilterNonEmptyPaths(tt.paths)
			
			if !reflect.DeepEqual(filtered, tt.wantFiltered) {
				t.Errorf("FilterNonEmptyPaths() filtered = %v, want %v", filtered, tt.wantFiltered)
			}
			
			if len(removed) != tt.wantRemovedLen {
				t.Errorf("FilterNonEmptyPaths() removed count = %d, want %d (removed: %v)", len(removed), tt.wantRemovedLen, removed)
			}
		})
	}
}
