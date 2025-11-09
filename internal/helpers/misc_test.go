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
