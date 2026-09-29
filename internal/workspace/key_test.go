package workspace

import "testing"

func TestParseKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want Key
		ok   bool
	}{
		{"api:build", Key{"api", "build"}, true},
		{"my.lib:ci.full", Key{"my.lib", "ci.full"}, true},
		{"core", Key{}, false},
		{":build", Key{}, false},
		{"api:", Key{}, false},
		{"a:b:c", Key{}, false},
	}
	for _, tt := range tests {
		got, ok := ParseKey(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseKey(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
	if s := (Key{"my.lib", "ci.full"}).String(); s != "my.lib:ci.full" {
		t.Errorf("String = %q", s)
	}
}

func TestValidSectionName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"build", "ci.full", "ci.pre-commit", "a_b.c-d", "0"} {
		if !ValidSectionName(name) {
			t.Errorf("%q rejected", name)
		}
	}
	for _, name := range []string{"", "Build", "ci.", ".ci", "ci..full", "-x", "a b", "a:b", "_x"} {
		if ValidSectionName(name) {
			t.Errorf("%q accepted", name)
		}
	}
}

func TestCompileKeyPattern(t *testing.T) {
	t.Parallel()
	re, err := CompileKeyPattern(`api:.*`)
	if err != nil {
		t.Fatal(err)
	}
	if !re.MatchString("api:build") || re.MatchString("webapi:build") {
		t.Error("pattern must match the whole key")
	}
	re, _ = CompileKeyPattern(`a|b:x`)
	if !re.MatchString("a") || re.MatchString("ab:x") {
		t.Error("alternation must be grouped before anchoring")
	}
	if _, err := CompileKeyPattern(`(`); err == nil {
		t.Error("invalid regex accepted")
	}
}
