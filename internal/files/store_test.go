package files

import "testing"


func TestHomeDir(t *testing.T) {
	for in, want := range map[string]string{
		"Contact@CloudLift.run": "contact@cloudlift.run",
		"a+b@example.com":       "a+b@example.com",
		"weird user/x@example.com": "weird_user_x@example.com",
		"":                         "user",
		"...":                      "user",
	} {
		if got := HomeDir(in); got != want {
			t.Errorf("HomeDir(%q) = %q, want %q", in, got, want)
		}
	}
}
