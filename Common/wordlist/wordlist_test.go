package wordlist

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestBuildIndexAndReadRange(t *testing.T) {
	lines := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	path := writeTempWordlist(t, lines)

	index, err := BuildIndex(path, 2, DefaultMaxLineBytes)
	if err != nil {
		t.Fatalf("BuildIndex error: %v", err)
	}
	if index.LineCount != int64(len(lines)) {
		t.Fatalf("expected %d lines, got %d", len(lines), index.LineCount)
	}

	reader, err := NewReader(index)
	if err != nil {
		t.Fatalf("NewReader error: %v", err)
	}
	defer reader.Close()

	var got []string
	err = reader.ReadRange(1, 4, func(line string, lineNumber int64) error {
		got = append(got, line)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRange error: %v", err)
	}
	want := lines[1:4]
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func BenchmarkBuildIndex(b *testing.B) {
	lines := make([]string, 10000)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%d", i)
	}
	path := writeTempWordlist(b, lines)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := BuildIndex(path, DefaultIndexStride, DefaultMaxLineBytes); err != nil {
			b.Fatalf("BuildIndex error: %v", err)
		}
	}
}

func BenchmarkReadRange(b *testing.B) {
	lines := make([]string, 10000)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%d", i)
	}
	path := writeTempWordlist(b, lines)
	index, err := BuildIndex(path, DefaultIndexStride, DefaultMaxLineBytes)
	if err != nil {
		b.Fatalf("BuildIndex error: %v", err)
	}
	reader, err := NewReader(index)
	if err != nil {
		b.Fatalf("NewReader error: %v", err)
	}
	defer reader.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := reader.ReadRange(0, int64(len(lines)), func(line string, lineNumber int64) error {
			return nil
		})
		if err != nil {
			b.Fatalf("ReadRange error: %v", err)
		}
	}
}

func writeTempWordlist(tb testing.TB, lines []string) string {
	tb.Helper()
	file, err := os.CreateTemp("", "wordlist-*.txt")
	if err != nil {
		tb.Fatalf("CreateTemp error: %v", err)
	}
	tb.Cleanup(func() {
		_ = os.Remove(file.Name())
	})
	defer file.Close()

	_, err = file.WriteString(strings.Join(lines, "\n"))
	if err != nil {
		tb.Fatalf("WriteString error: %v", err)
	}
	return file.Name()
}
