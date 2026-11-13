package yamlx

import "testing"

func TestFlowMap(t *testing.T) {
	m, err := Unmarshal([]byte("policy:\n  quality: {minimum: 0.9, strict: true}\n"))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	pol := GetMap(m, "policy")
	q := GetMap(pol, "quality")
	if q == nil {
		t.Fatal("quality did not parse as a flow map")
	}
	if got := GetFloat(q, "minimum", -1); got != 0.9 {
		t.Errorf("minimum = %v, want 0.9", got)
	}
	if !GetBool(q, "strict") {
		t.Error("strict = false, want true")
	}
}

func TestFlowSeq(t *testing.T) {
	m, err := Unmarshal([]byte("providers: [ollama, openai, anthropic]\n"))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s := GetSlice(m, "providers")
	if len(s) != 3 {
		t.Fatalf("len = %d, want 3", len(s))
	}
	if s[0] != "ollama" || s[2] != "anthropic" {
		t.Errorf("unexpected seq contents: %v", s)
	}
}

func TestEmptyFlowCollections(t *testing.T) {
	m, err := Unmarshal([]byte("tools: []\nextra: {}\n"))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s := GetSlice(m, "tools"); len(s) != 0 {
		t.Errorf("tools len = %d, want 0", len(s))
	}
	if mm := GetMap(m, "extra"); mm == nil || len(mm) != 0 {
		t.Errorf("extra = %v, want empty map", mm)
	}
}

func TestStrictRejectsTab(t *testing.T) {
	_, err := Unmarshal([]byte("policy:\n\tquality: high\n"))
	if err == nil {
		t.Fatal("expected error for tab indentation, got nil")
	}
}

func TestStrictRejectsMissingColon(t *testing.T) {
	_, err := Unmarshal([]byte("name: ok\nthis line has no colon\n"))
	if err == nil {
		t.Fatal("expected error for missing colon, got nil")
	}
}

func TestStrictRejectsDuplicateKey(t *testing.T) {
	_, err := Unmarshal([]byte("name: a\nname: b\n"))
	if err == nil {
		t.Fatal("expected error for duplicate key, got nil")
	}
}

func TestValidSpecStillParses(t *testing.T) {
	src := "name: x\nversion: \"1.0\"\npolicy:\n  quality: high\n  max_retries: 2\ntools:\n  - name: t1\n    description: d1\n"
	m, err := Unmarshal([]byte(src))
	if err != nil {
		t.Fatalf("valid spec failed to parse: %v", err)
	}
	if GetString(m, "name") != "x" {
		t.Error("name mismatch")
	}
	if GetString(GetMap(m, "policy"), "quality") != "high" {
		t.Error("policy.quality mismatch")
	}
}
