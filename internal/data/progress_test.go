package data

import "testing"

func TestSanitizeStripsEscapeSequences(t *testing.T) {
	cases := map[string]string{
		"a\x1b[31mred\x1b[0m":         "ared",
		"x\x1b]0;title\x07y":          "xy",
		"x\x1b]8;;http://e\x1b\\link": "xlink",
		"k\x1bPpayload\x1b\\v":        "kv",
		"tab\there":                   "tab    here",
		"bell\x07\x00\u009bdone":      "belldone",
		"keep é ✓":                    "keep é ✓",
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q want %q", in, got, want)
		}
	}
}

func TestPodKey(t *testing.T) {
	p := Pod{Name: "web-0", Namespace: "apps"}
	if p.Key() != "apps/web-0" || PodKey("staging", "web-0") == p.Key() {
		t.Fatal("pod keys must include the namespace")
	}
}
