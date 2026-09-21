package jsonx

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateRejectsDuplicateKnownAndUnknownKeys(t *testing.T) {
	for _, input := range []string{
		`{"format_version":1,"format_version":1}`,
		`{"known":1,"unknown":2,"unknown":3}`,
		`{"nested":{"value":1,"value":2}}`,
	} {
		if err := Validate([]byte(input)); err == nil || !strings.Contains(err.Error(), "duplicate key") {
			t.Fatalf("Validate(%q) error=%v", input, err)
		}
	}
	var out struct {
		Known int `json:"known"`
	}
	if err := Decode([]byte(`{"known":1,"unknown":2}`), &out); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error=%v", err)
	}
}

func TestValidateRejectsMalformedAndTrailingDocuments(t *testing.T) {
	for _, input := range []string{`{"key":`, `{"key":1`, `{"key":1} {"other":2}`, `]`} {
		if err := Validate([]byte(input)); err == nil {
			t.Fatalf("Validate(%q) unexpectedly succeeded", input)
		}
	}
}

func TestValidateManyDistinctKeys(t *testing.T) {
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i < 5000; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"key-%d":%d`, i, i)
	}
	b.WriteByte('}')
	if err := Validate([]byte(b.String())); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkValidateDistinctKeys(b *testing.B) {
	var data strings.Builder
	data.WriteByte('{')
	for i := 0; i < 2000; i++ {
		if i > 0 {
			data.WriteByte(',')
		}
		fmt.Fprintf(&data, `"key-%d":%d`, i, i)
	}
	data.WriteByte('}')
	payload := []byte(data.String())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := Validate(payload); err != nil {
			b.Fatal(err)
		}
	}
}
