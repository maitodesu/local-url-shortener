package encode

import (
	"testing"
)

type testCase struct {
	input    int64
	expected string
}

var testData []testCase = []testCase{
	{342, "w5"},
	{1, "1"},
	{23423, "N56"},
}

func TestEncode(t *testing.T) {
	i := 0
	for i < len(testData) {
		tt := testData[i]
		id, want := Encode(tt.input), tt.expected
		if id != want {
			t.Errorf("Incorrect encoding")
		}
		i += 1
	}
}
