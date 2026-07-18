package cli

import "testing"

func TestExtractBrowserExecutable(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "separate", args: []string{"convert", "url", "--browser-executable", "/opt/chromium"}, want: "/opt/chromium"},
		{name: "equals", args: []string{"browser", "status", "--browser-executable=/opt/chrome"}, want: "/opt/chrome"},
		{name: "absent", args: []string{"version"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ExtractBrowserExecutable(test.args); got != test.want {
				t.Fatalf("ExtractBrowserExecutable = %q, want %q", got, test.want)
			}
		})
	}
}
