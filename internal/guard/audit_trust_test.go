package guard

import (
	"errors"
	"testing"
)

func TestAuditTrustDecisions(t *testing.T) {
	const self, other, root = 1000, 1001, 0
	for name, tc := range map[string]struct {
		node auditNode
		want bool
	}{
		"root 0755":            {auditNode{root, 0o755}, true},
		"self 0755":            {auditNode{self, 0o755}, true},
		"self 0700":            {auditNode{self, 0o700}, true},
		"other 0755":           {auditNode{other, 0o755}, false},
		"overflow uid":         {auditNode{65534, 0o755}, false},
		"self group write":     {auditNode{self, 0o775}, false},
		"root group write":     {auditNode{root, 0o775}, false},
		"self others write":    {auditNode{self, 0o702}, false},
		"root sticky":          {auditNode{root, 0o1777}, true},
		"self sticky group":    {auditNode{self, 0o1770}, true},
		"other sticky":         {auditNode{other, 0o1777}, false},
		"setgid is not sticky": {auditNode{self, 0o2775}, false},
	} {
		if got := trustedDir(tc.node, self); got != tc.want {
			t.Errorf("dir %s: %v", name, got)
		}
	}
	for name, tc := range map[string]struct {
		parent, link auditNode
		want         bool
	}{
		"root in root":            {auditNode{root, 0o755}, auditNode{root, 0o777}, true},
		"root in private root":    {auditNode{root, 0o700}, auditNode{root, 0o777}, true},
		"root in self":            {auditNode{self, 0o700}, auditNode{root, 0o777}, false},
		"root in root group":      {auditNode{root, 0o775}, auditNode{root, 0o777}, false},
		"root in root sticky":     {auditNode{root, 0o1777}, auditNode{root, 0o777}, false},
		"root in sticky no write": {auditNode{root, 0o1755}, auditNode{root, 0o777}, true},
		"self in root":            {auditNode{root, 0o755}, auditNode{self, 0o777}, false},
		"self in self":            {auditNode{self, 0o700}, auditNode{self, 0o777}, false},
		"other in root":           {auditNode{root, 0o755}, auditNode{other, 0o777}, false},
	} {
		if got := trustedLink(tc.parent, tc.link); got != tc.want {
			t.Errorf("link %s: %v", name, got)
		}
	}
	for name, tc := range map[string]struct {
		node auditNode
		want bool
	}{
		"self 0700":      {auditNode{self, 0o700}, true},
		"self umask":     {auditNode{self, 0o300}, true},
		"self inherited": {auditNode{self, 0o770}, false},
		"root 0700":      {auditNode{root, 0o700}, false},
		"other 0700":     {auditNode{other, 0o700}, false},
	} {
		if got := createdDir(tc.node, self); got != tc.want {
			t.Errorf("created %s: %v", name, got)
		}
	}
}

func TestAuditRefusalBlocksReaderAndWriter(t *testing.T) {
	for _, r := range []AuditRefusal{RefusedPlatform, RefusedFilesystem, RefusedLink, RefusedOwner, RefusedPrivate, RefusedForm} {
		if !errors.Is(r, ErrAuditUnreadable) || !errors.Is(r, AuditFailed) || errors.Is(r, Denied) || r.Error() == "" {
			t.Errorf("%q must fail closed for both consumers", r)
		}
	}
}
