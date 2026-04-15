package master

import (
	"reflect"
	"testing"
)

func TestSplitArgsSupportsQuotesAndEscapes(t *testing.T) {
	got, err := splitArgs(`task cancel --reason "lab abort" task-1 escaped\ value`)
	if err != nil {
		t.Fatalf("splitArgs returned error: %v", err)
	}
	want := []string{"task", "cancel", "--reason", "lab abort", "task-1", "escaped value"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %#v, got %#v", want, got)
	}
}

func TestSplitArgsRejectsUnterminatedQuote(t *testing.T) {
	if _, err := splitArgs(`task cancel --reason "unfinished`); err == nil {
		t.Fatal("splitArgs accepted unterminated quote")
	}
}

func TestIsShellLikeCommand(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{args: []string{"go", "run", "./Master"}, want: true},
		{args: []string{"cd", "/tmp"}, want: true},
		{args: []string{"./Master", "serve"}, want: true},
		{args: []string{"../Cerberus/Master"}, want: true},
		{args: []string{"/bin/ls"}, want: true},
		{args: []string{"task", "list"}, want: false},
		{args: []string{"worker", "list"}, want: false},
		{args: []string{"dispatch", "pause"}, want: false},
		{args: []string{"-h"}, want: false},
	}
	for _, tc := range cases {
		if got := isShellLikeCommand(tc.args); got != tc.want {
			t.Fatalf("isShellLikeCommand(%#v) = %t, want %t", tc.args, got, tc.want)
		}
	}
}
