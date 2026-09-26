package workspace

import (
	"cmp"
	"regexp"
	"strings"
)

// Key names one section of one project.
type Key struct {
	Project string
	Section string
}

// String returns "<project>:<section>".
func (k Key) String() string { return k.Project + ":" + k.Section }

// ParseKey splits s at its only ":". Both parts must be non-empty.
func ParseKey(s string) (Key, bool) {
	project, section, ok := strings.Cut(s, ":")
	if !ok || project == "" || section == "" || strings.Contains(section, ":") {
		return Key{}, false
	}
	return Key{Project: project, Section: section}, true
}

func compareKeys(a, b Key) int {
	return cmp.Or(strings.Compare(a.Project, b.Project), strings.Compare(a.Section, b.Section))
}

var segmentRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ValidSectionName reports whether name is "."-joined segments that each match [a-z0-9][a-z0-9_-]*.
func ValidSectionName(name string) bool {
	for seg := range strings.SplitSeq(name, ".") {
		if !segmentRE.MatchString(seg) {
			return false
		}
	}
	return true
}

// CompileKeyPattern compiles an RE2 pattern that must match a whole "<project>:<section>" key.
func CompileKeyPattern(p string) (*regexp.Regexp, error) {
	if _, err := regexp.Compile(p); err != nil {
		return nil, err
	}
	return regexp.Compile("^(?:" + p + ")$")
}
