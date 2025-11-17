package helpers

import (
	"os"
	"strings"
)

func ParseBool(v string) bool {
	return strings.EqualFold(v, "true") || v == "1" || strings.EqualFold(v, "yes")
}

func SplitCSV(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// TruncateString safely truncates a string to a maximum length.
// If the string is shorter than maxLen, it returns the original string.
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// FilterNonEmptyPaths filters out paths that don't exist or have size 0.
// Returns the filtered paths and a list of removed paths with their sizes.
func FilterNonEmptyPaths(paths []string) (filtered []string, removed []string) {
	filtered = make([]string, 0, len(paths))
	removed = make([]string, 0)
	
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			// Path doesn't exist or can't be accessed, skip it
			removed = append(removed, path)
			continue
		}
		
		// For directories, include them (they might contain files)
		if info.IsDir() {
			filtered = append(filtered, path)
			continue
		}
		
		// For files, only include if size > 0
		if info.Size() > 0 {
			filtered = append(filtered, path)
		} else {
			removed = append(removed, path)
		}
	}
	
	return filtered, removed
}
