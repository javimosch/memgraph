package cloudserver

import (
	"os/exec"
	"regexp"
	"strconv"
	"sync"
)

var gitVersionNumber = regexp.MustCompile(`^git version (\d+)\.(\d+)`)

// durabilityFor returns the settings that make this git flush what it writes.
// core.fsync arrived in git 2.36 and covers refs too. It deprecates
// core.fsyncObjectFiles, the only choice before, which covers loose objects.
// An unreadable version gets both, and git ignores what it does not know.
func durabilityFor(version string) [][2]string {
	older := [2]string{"core.fsyncObjectFiles", "true"}
	newer := [][2]string{{"core.fsync", "all"}, {"core.fsyncMethod", "fsync"}}
	m := gitVersionNumber.FindStringSubmatch(version)
	if m == nil {
		return append(newer, older)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major > 2 || (major == 2 && minor >= 36) {
		return newer
	}
	return [][2]string{older}
}

var gitDurability = sync.OnceValue(func() [][2]string {
	out, _ := exec.Command("git", "version").Output()
	return durabilityFor(string(out))
})
