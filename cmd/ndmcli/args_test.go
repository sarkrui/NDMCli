package main

import "testing"

func TestReorderArgs(t *testing.T) {
	flags, pos := reorderArgs([]string{"12", "-n", "4", "--adopt", "-o", "out.bin"}, map[string]bool{"n": true, "o": true})
	if len(pos) != 1 || pos[0] != "12" {
		t.Fatalf("pos %v", pos)
	}
	joined := stringsJoin(flags)
	if joined != "-n 4 --adopt -o out.bin" {
		t.Fatalf("flags %q", joined)
	}
	flags, pos = reorderArgs([]string{"--status=paused", "nope"}, map[string]bool{"status": true})
	if len(pos) != 1 || pos[0] != "nope" {
		t.Fatalf("pos %v flags %v", pos, flags)
	}
}

func stringsJoin(v []string) string {
	out := ""
	for i, s := range v {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}
