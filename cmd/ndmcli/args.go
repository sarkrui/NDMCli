package main

import "strings"

// reorderArgs pulls flags out from anywhere in the argument list so
// `ndmcli download 12 -n 4` works. Go's flag package stops at the first
// non-flag otherwise. takesValue is keyed by the flag name without dashes.
func reorderArgs(args []string, takesValue map[string]bool) (flags, pos []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		if strings.Contains(a, "=") {
			continue
		}
		name := strings.TrimLeft(a, "-")
		if takesValue[name] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return flags, pos
}
