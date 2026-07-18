package cli

import (
	"net/url"
	"strings"
)

func isHTTPURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// interspersed moves known flags before positionals so the standard flag
// package accepts the documented "command <value> [options]" form.
func interspersed(args []string, valueFlags map[string]bool) []string {
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		name := arg
		if equals := strings.IndexByte(name, '='); equals >= 0 {
			name = name[:equals]
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			if valueFlags[name] && !strings.Contains(arg, "=") && index+1 < len(args) {
				index++
				flags = append(flags, args[index])
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	return append(flags, positionals...)
}

// ExtractConfigDir finds the portable state override before the backend is
// constructed. The option remains in args so command-specific help is intact.
func ExtractConfigDir(args []string) string {
	for index, arg := range args {
		if strings.HasPrefix(arg, "--config-dir=") {
			return strings.TrimPrefix(arg, "--config-dir=")
		}
		if arg == "--config-dir" && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

// ExtractBrowserExecutable finds the browser override before the backend is
// constructed. The option remains in args for command-specific parsing.
func ExtractBrowserExecutable(args []string) string {
	for index, arg := range args {
		if strings.HasPrefix(arg, "--browser-executable=") {
			return strings.TrimPrefix(arg, "--browser-executable=")
		}
		if arg == "--browser-executable" && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}
