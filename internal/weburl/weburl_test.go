package weburl

import (
	"errors"
	"strings"
	"testing"
)

func TestNewAcceptsOnlyCanonicalAddresses(t *testing.T) {
	for _, tc := range []struct {
		scheme, host string
		port         int
		path         string
		want         string
		reach        Reach
	}{
		{"https", "web.example.com", 443, "/", "https://web.example.com", ReachPublic},
		{"http", "web.example.com", 80, "/api/v1", "http://web.example.com/api/v1", ReachPublic},
		{"http", "web.example.com", 8080, "/", "http://web.example.com:8080", ReachPublic},
		{"https", "web.example.com", 80, "/", "https://web.example.com:80", ReachPublic},
		{"https", "xn--pple-43d.com", 443, "/", "https://xn--pple-43d.com", ReachPublic},
		{"http", "203.0.113.10", 80, "/", "http://203.0.113.10", ReachPublic},
		{"http", "10.0.0.5", 30080, "/", "http://10.0.0.5:30080", ReachPrivate},
		{"http", "100.64.1.1", 80, "/", "http://100.64.1.1", ReachPrivate},
		{"http", "fd00::10", 80, "/", "http://[fd00::10]", ReachPrivate},
		{"https", "2001:db8::1", 8443, "/x", "https://[2001:db8::1]:8443/x", ReachPublic},
		{"http", "web.apps.svc", 80, "/", "http://web.apps.svc", ReachCluster},
		{"http", "web.apps.svc.cluster.local", 80, "/", "http://web.apps.svc.cluster.local", ReachCluster},
		{"http", "web.corp", 80, "/", "http://web.corp", ReachPrivate},
		{"http", "a.b", 80, "/x/", "http://a.b/x/", ReachPublic},
	} {
		u, err := New(tc.scheme, tc.host, tc.port, tc.path)
		if err != nil || u.String() != tc.want || u.Reach() != tc.reach || u.Host() != tc.host {
			t.Errorf("%v %v: got %q reach %v err %v", tc.host, tc.path, u.String(), u.Reach(), err)
		}
		if u.Linkable() != (tc.reach != ReachCluster) {
			t.Errorf("%s: linkable=%v", tc.want, u.Linkable())
		}
	}
}

func TestNewRejectsHostileOrAmbiguousInput(t *testing.T) {
	long := strings.Repeat("a", 64) + ".com"
	for _, tc := range []struct {
		name, scheme, host string
		port               int
		path               string
		want               error
	}{
		{"javascript", "javascript", "a.com", 80, "/", ErrScheme},
		{"upper scheme", "HTTP", "a.com", 80, "/", ErrScheme},
		{"file", "file", "a.com", 80, "/", ErrScheme},
		{"empty host", "http", "", 80, "/", ErrEmptyHost},
		{"wildcard", "http", "*.a.com", 80, "/", ErrWildcard},
		{"bare wildcard", "http", "*", 80, "/", ErrWildcard},
		{"cyrillic homograph", "http", "\u0430pple.com", 80, "/", ErrNonASCII},
		{"mixed scripts", "http", "payp\u0430l.com", 80, "/", ErrNonASCII},
		{"fullwidth dot", "http", "a\u3002com", 80, "/", ErrNonASCII},
		{"bidi override", "http", "a\u202ecom.evil", 80, "/", ErrNonASCII},
		{"zero width", "http", "a\u200b.com", 80, "/", ErrNonASCII},
		{"c1 control", "http", "a\u009d.com", 80, "/", ErrNonASCII},
		{"userinfo", "http", "user@evil.com", 80, "/", ErrHost},
		{"userinfo password", "http", "u:p@evil.com", 80, "/", ErrHost},
		{"backslash", "http", "a.com\\evil.com", 80, "/", ErrHost},
		{"slash", "http", "a.com/evil", 80, "/", ErrHost},
		{"query", "http", "a.com?x", 80, "/", ErrHost},
		{"fragment", "http", "a.com#x", 80, "/", ErrHost},
		{"percent", "http", "a%2ecom.b", 80, "/", ErrHost},
		{"crlf", "http", "a.com\r\nb", 80, "/", ErrHost},
		{"esc", "http", "a\x1b]52;c;x\x07.com", 80, "/", ErrHost},
		{"bel", "http", "a\x07.com", 80, "/", ErrHost},
		{"st", "http", "a\x1b\\.com", 80, "/", ErrHost},
		{"del", "http", "a\x7f.com", 80, "/", ErrHost},
		{"space", "http", "a .com", 80, "/", ErrHost},
		{"semicolon", "http", "a;b.com", 80, "/", ErrHost},
		{"upper", "http", "A.com", 80, "/", ErrHost},
		{"underscore", "http", "a_b.com", 80, "/", ErrHost},
		{"trailing dot", "http", "a.com.", 80, "/", ErrHost},
		{"double dot", "http", "a..com", 80, "/", ErrHost},
		{"leading dash", "http", "-a.com", 80, "/", ErrHost},
		{"long label", "http", long, 80, "/", ErrHost},
		{"long host", "http", strings.Repeat("a.", 130) + "com", 80, "/", ErrHost},
		{"decimal ip", "http", "2130706433", 80, "/", ErrAmbiguous},
		{"short ip", "http", "127.1", 80, "/", ErrAmbiguous},
		{"octal ip", "http", "0177.0.0.1", 80, "/", ErrAmbiguous},
		{"hex ip", "http", "0x7f000001", 80, "/", ErrAmbiguous},
		{"hex last label", "http", "a.0x1", 80, "/", ErrAmbiguous},
		{"numeric last label", "http", "a.b.5", 80, "/", ErrAmbiguous},
		{"localhost", "http", "localhost", 80, "/", ErrLocal},
		{"sub localhost", "http", "a.localhost", 80, "/", ErrLocal},
		{"loopback", "http", "127.0.0.1", 80, "/", ErrLocal},
		{"loopback6", "http", "::1", 80, "/", ErrLocal},
		{"unspecified", "http", "0.0.0.0", 80, "/", ErrLocal},
		{"this network", "http", "0.1.2.3", 80, "/", ErrLocal},
		{"metadata", "http", "169.254.169.254", 80, "/", ErrLocal},
		{"link local 6", "http", "fe80::1", 80, "/", ErrLocal},
		{"multicast", "http", "224.0.0.1", 80, "/", ErrLocal},
		{"broadcast", "http", "255.255.255.255", 80, "/", ErrLocal},
		{"nat64 loopback", "http", "64:ff9b::7f00:1", 80, "/", ErrLocal},
		{"mapped loopback", "http", "::ffff:127.0.0.1", 80, "/", ErrAmbiguous},
		{"expanded ipv6", "http", "2001:0db8::1", 80, "/", ErrAmbiguous},
		{"zone", "http", "fe80::1%eth0", 80, "/", ErrHost},
		{"bracketed", "http", "[2001:db8::1]", 80, "/", ErrHost},
		{"host port", "http", "a.com:81", 80, "/", ErrHost},
		{"single label", "http", "intranet", 80, "/", ErrShort},
		{"port zero", "http", "a.com", 0, "/", ErrPort},
		{"port high", "http", "a.com", 70000, "/", ErrPort},
		{"port negative", "http", "a.com", -1, "/", ErrPort},
		{"empty path", "http", "a.com", 80, "", ErrPath},
		{"relative path", "http", "a.com", 80, "x", ErrPath},
		{"network path", "http", "a.com", 80, "//evil.com", ErrPath},
		{"inner empty", "http", "a.com", 80, "/a//b", ErrPath},
		{"dot dot", "http", "a.com", 80, "/a/../b", ErrPath},
		{"dot", "http", "a.com", 80, "/./b", ErrPath},
		{"regex", "http", "a.com", 80, "/api(/|$)(.*)", ErrPath},
		{"path at", "http", "a.com", 80, "/@evil.com", ErrPath},
		{"path backslash", "http", "a.com", 80, "/\\evil.com", ErrPath},
		{"path crlf", "http", "a.com", 80, "/a\r\nb", ErrPath},
		{"path esc", "http", "a.com", 80, "/a\x1b]8;;http://evil\x07", ErrPath},
		{"path bel", "http", "a.com", 80, "/a\x07", ErrPath},
		{"path st", "http", "a.com", 80, "/a\x1b\\", ErrPath},
		{"path encoded newline", "http", "a.com", 80, "/a%0d%0ab", ErrPath},
		{"path encoded slash", "http", "a.com", 80, "/a%2fb", ErrPath},
		{"path double encoded", "http", "a.com", 80, "/a%252e", ErrPath},
		{"path bidi", "http", "a.com", 80, "/a\u202eb", ErrPath},
		{"path c1 osc", "http", "a.com", 80, "/a\u009d8;;", ErrPath},
		{"path query", "http", "a.com", 80, "/a?b=c", ErrPath},
		{"path fragment", "http", "a.com", 80, "/a#b", ErrPath},
		{"path semicolon", "http", "a.com", 80, "/a;b", ErrPath},
		{"path wildcard", "http", "a.com", 80, "/*", ErrPath},
		{"path long", "http", "a.com", 80, "/" + strings.Repeat("a", 1024), ErrPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, err := New(tc.scheme, tc.host, tc.port, tc.path)
			if !errors.Is(err, tc.want) || !u.IsZero() || u.Linkable() {
				t.Fatalf("got %q err=%v want %v", u.String(), err, tc.want)
			}
		})
	}
}

func TestPunycodeStaysVisible(t *testing.T) {
	u, err := New("https", "www.xn--pple-43d.com", 443, "/")
	if err != nil || !u.Punycode() || !strings.Contains(u.String(), "xn--") {
		t.Fatalf("punycode must stay in its ASCII form: %q %v", u.String(), err)
	}
	if plain, _ := New("https", "www.example.com", 443, "/"); plain.Punycode() {
		t.Fatal("plain host is not punycode")
	}
}

func FuzzNewNeverEmitsControls(f *testing.F) {
	for _, s := range []string{"a.com", "a\x1b.com", "\u0430.com", "127.1", "[::1]", "a@b.com", "*.a"} {
		f.Add(s, "/x")
	}
	f.Fuzz(func(t *testing.T, host, path string) {
		u, err := New("https", host, 443, path)
		if err != nil {
			if !u.IsZero() {
				t.Fatal("a rejected URL is never usable")
			}
			return
		}
		for i := 0; i < len(u.String()); i++ {
			if c := u.String()[i]; c <= 0x20 || c >= 0x7f || c == '@' || c == '\\' || c == ';' || c == '%' {
				t.Fatalf("unsafe byte %q in %q", c, u.String())
			}
		}
	})
}
