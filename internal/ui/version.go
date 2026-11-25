package ui

import (
	"regexp"
	"runtime/debug"
	"strings"
)

const (
	devVersion       = "dev"
	versionLimit     = 64
	versionHeaderMax = 18
	commitShort      = 7
)

var Version = ""

var (
	releaseVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
	pseudoVersion  = regexp.MustCompile(`^v[0-9]+\.(0\.0-|[0-9]+\.[0-9]+-([^+]*\.)?0\.)[0-9]{14}-([0-9a-f]{12})(\+.*)?$`)
	revisionFormat = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
)

func init() {
	info, _ := debug.ReadBuildInfo()
	Version = resolveVersion(Version, info)
}

func resolveVersion(linked string, info *debug.BuildInfo) string {
	if v := cleanVersion(linked); v != "" {
		return v
	}
	if info == nil {
		return devVersion
	}
	settings := make(map[string]string, len(info.Settings))
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	dirty := settings["vcs.modified"] == "true" || strings.HasSuffix(info.Main.Version, "+dirty")
	if v := info.Main.Version; !dirty && len(v) <= versionLimit && releaseVersion.MatchString(v) && !pseudoVersion.MatchString(v) {
		return v
	}
	out := devVersion
	if revision := moduleRevision(info.Main.Version, settings["vcs.revision"]); revision != "" {
		out += "-" + revision
	}
	if dirty {
		out += "-dirty"
	}
	return out
}

func moduleRevision(version, revision string) string {
	if !revisionFormat.MatchString(revision) {
		m := pseudoVersion.FindStringSubmatch(version)
		if m == nil {
			return ""
		}
		revision = m[3]
	}
	return revision[:commitShort]
}

func cleanVersion(v string) string {
	v = strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', strings.ContainsRune("._+-", r):
			return r
		}
		return -1
	}, v)
	return v[:min(len(v), versionLimit)]
}
