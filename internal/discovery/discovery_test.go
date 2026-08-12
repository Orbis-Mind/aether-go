package discovery

import "testing"

func TestBaseName(t *testing.T) {
	if got := baseName("llama3.2:latest"); got != "llama3.2" {
		t.Errorf("baseName tag-strip failed: %s", got)
	}
	if got := baseName("mistral"); got != "mistral" {
		t.Errorf("baseName should pass through untagged: %s", got)
	}
}

func TestMatchKey(t *testing.T) {
	cases := map[string]string{
		"llama3.2:latest": "llama3.2",
		"qwen2.5:7b":      "qwen2.5",
		"gemma2:2b":       "gemma2",
		"nonexistent:1b":  "",
	}
	for installed, want := range cases {
		if got := matchKey(installed); got != want {
			t.Errorf("matchKey(%q) = %q, want %q", installed, got, want)
		}
	}
}
