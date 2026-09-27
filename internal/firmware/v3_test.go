package firmware

import (
	"os"
	"testing"
)

func TestPatchV3KnownOfficialBase(t *testing.T) {
	path := os.Getenv("MU3C_OFFICIAL_ROM")
	if path == "" {
		t.Skip("set MU3C_OFFICIAL_ROM to the owner-supplied official firmware")
	}
	base, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	patched, err := PatchV3(base)
	if err != nil {
		t.Fatal(err)
	}
	if Hash(patched) != V3SHA256 {
		t.Fatal("patch result differs from the validated v3 ROM")
	}
	base[100] ^= 1
	if _, err := PatchV3(base); err == nil {
		t.Fatal("modified upstream base was accepted")
	}
}
