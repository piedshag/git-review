package cli

import (
	"flag"
	"strings"
)

type boolFlag interface {
	IsBoolFlag() bool
}

// ParseInterspersed lets flags appear before or after positional arguments.
// The standard flag package stops parsing at the first positional argument.
func ParseInterspersed(fs *flag.FlagSet, arguments []string) error {
	flags := make([]string, 0, len(arguments))
	positionals := make([]string, 0, 1)
	for i := 0; i < len(arguments); i++ {
		argument := arguments[i]
		if argument == "--" {
			positionals = append(positionals, arguments[i+1:]...)
			break
		}
		if argument == "-" || !strings.HasPrefix(argument, "-") {
			positionals = append(positionals, argument)
			continue
		}

		flags = append(flags, argument)
		name := strings.TrimLeft(argument, "-")
		if before, _, found := strings.Cut(name, "="); found {
			name = before
			continue
		}
		definition := fs.Lookup(name)
		if definition == nil {
			continue
		}
		if boolean, ok := definition.Value.(boolFlag); ok && boolean.IsBoolFlag() {
			continue
		}
		if i+1 < len(arguments) {
			i++
			flags = append(flags, arguments[i])
		}
	}
	normalized := append(flags, "--")
	normalized = append(normalized, positionals...)
	return fs.Parse(normalized)
}
