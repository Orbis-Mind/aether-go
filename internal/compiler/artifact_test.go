package compiler

import (
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// compileFixture compiles a minimal valid spec into dir and returns the
// artifact path.
func compileFixture(t *testing.T, dir string) string {
	t.Helper()
	specPath := writeTemp(t, "name: fixture\nversion: \"1.0\"\n")
	spec, err := ValidateYAML(specPath)
	if err != nil {
		t.Fatalf("ValidateYAML: %v", err)
	}
	res, err := Compile(spec, dir, false)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return res.ArtifactPath
}

// TestCompileDeterministic verifies the same spec compiles to byte-identical
// artifacts (no embedded random ID or wall-clock timestamp).
func TestCompileDeterministic(t *testing.T) {
	d1, d2 := t.TempDir(), t.TempDir()
	a := compileFixture(t, d1)
	b := compileFixture(t, d2)

	ba, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := os.ReadFile(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(ba) != string(bb) {
		t.Fatalf("compilation is not deterministic: artifacts differ (len %d vs %d)", len(ba), len(bb))
	}
	if sha256.Sum256(ba) != sha256.Sum256(bb) {
		t.Fatal("artifact checksums differ across identical compiles")
	}
}

// TestArtifactRoundTrip verifies a compiled artifact reads back with verified
// integrity.
func TestArtifactRoundTrip(t *testing.T) {
	path := compileFixture(t, t.TempDir())
	ac, err := ReadAEArtifact(path)
	if err != nil {
		t.Fatalf("ReadAEArtifact: %v", err)
	}
	if ac.Spec.Name != "fixture" {
		t.Errorf("round-trip spec name = %q, want fixture", ac.Spec.Name)
	}
	if v, _ := ac.Manifest["artifact_version"].(string); v != "2" {
		t.Errorf("artifact_version = %q, want 2", v)
	}
}

// TestCorruptArtifactNoPanic is the regression test for the OOB-slice crash:
// a header that declares a payload larger than the file must error, not panic.
func TestCorruptArtifactNoPanic(t *testing.T) {
	cases := map[string][]byte{
		"empty":                 {},
		"magic only":            []byte("AETHER\x00\x02"),
		"bad magic":             append([]byte("NOTAET\x00\x02"), make([]byte, 40)...),
		"unsupported version":   append([]byte("AETHER\x00\x09"), make([]byte, 40)...),
		"oversized declared v2": buildFrame(0x02, 0xFFFFFFFF, make([]byte, 32), nil),
		"oversized declared v1": buildFrameV1Raw(0xFFFFFFFF),
		"truncated header":      []byte("AETHER\x00\x02\x00\x00"),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			// Must not panic; must return an error.
			if _, err := ReadAEArtifact(writeBytes(t, data)); err == nil {
				t.Errorf("expected error for %q, got nil", name)
			}
		})
	}
}

// TestTamperedPayloadFailsIntegrity verifies a single flipped payload byte is
// caught by the v2 checksum.
func TestTamperedPayloadFailsIntegrity(t *testing.T) {
	path := compileFixture(t, t.TempDir())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) <= aeHeaderV2 {
		t.Fatalf("artifact unexpectedly small: %d bytes", len(data))
	}
	data[aeHeaderV2] ^= 0xFF // flip first payload byte
	if _, err := ReadAEArtifact(writeBytes(t, data)); err == nil {
		t.Fatal("tampered payload passed integrity check")
	}
}

// TestV1BackwardCompatibility verifies the hardened reader still accepts legacy
// v1 artifacts (truncated checksum), per the v2+read-v1 compatibility policy.
func TestV1BackwardCompatibility(t *testing.T) {
	// Take a real v2 payload and re-frame it as v1.
	path := compileFixture(t, t.TempDir())
	v2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	payload := v2[aeHeaderV2:]
	sum := sha256.Sum256(payload)
	v1 := buildFrame(0x01, uint32(len(payload)), sum[:4], payload)

	ac, err := ReadAEArtifact(writeBytes(t, v1))
	if err != nil {
		t.Fatalf("reading v1 artifact: %v", err)
	}
	if ac.Spec.Name != "fixture" {
		t.Errorf("v1 round-trip name = %q, want fixture", ac.Spec.Name)
	}

	// A v1 frame with a wrong truncated checksum must be rejected.
	bad := buildFrame(0x01, uint32(len(payload)), []byte{0, 0, 0, 0}, payload)
	if _, err := ReadAEArtifact(writeBytes(t, bad)); err == nil {
		t.Error("v1 with bad checksum passed integrity check")
	}
}

// FuzzReadAEArtifact asserts the reader never panics on arbitrary input.
func FuzzReadAEArtifact(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("AETHER\x00\x02"))
	f.Add(append([]byte("AETHER\x00\x02"), make([]byte, 64)...))
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "fuzz.ae")
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Skip()
		}
		_, _ = ReadAEArtifact(path) // must not panic
	})
}

// ─── helpers ───

func writeBytes(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "artifact.ae")
	if err := os.WriteFile(p, data, 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

// buildFrame assembles an .ae frame with the given version, declared size,
// checksum bytes, and payload (payload may be nil to produce a header-only,
// oversized-declaration frame).
func buildFrame(version byte, declaredSize uint32, checksum, payload []byte) []byte {
	out := []byte("AETHER\x00\x00")
	out[7] = version
	var sz [4]byte
	binary.BigEndian.PutUint32(sz[:], declaredSize)
	out = append(out, sz[:]...)
	out = append(out, checksum...)
	out = append(out, payload...)
	return out
}

// buildFrameV1Raw builds a v1 header-only frame declaring a huge payload.
func buildFrameV1Raw(declaredSize uint32) []byte {
	return buildFrame(0x01, declaredSize, []byte{0, 0, 0, 0}, nil)
}
