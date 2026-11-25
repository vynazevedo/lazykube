package weburl

import (
	"errors"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

const (
	maxHost = 253
	maxPath = 1024
)

var (
	ErrScheme    = errors.New("esquema não é http nem https")
	ErrHost      = errors.New("host inválido")
	ErrWildcard  = errors.New("host curinga não é um endereço")
	ErrEmptyHost = errors.New("host vazio não é um endereço")
	ErrNonASCII  = errors.New("host com caracteres fora de ASCII")
	ErrAmbiguous = errors.New("host numérico ambíguo")
	ErrLocal     = errors.New("host local ou reservado")
	ErrShort     = errors.New("host sem domínio")
	ErrPort      = errors.New("porta inválida")
	ErrPath      = errors.New("path não é literal")
	ErrCanonical = errors.New("URL não é canônica")
)

type Reach int

const (
	ReachPublic Reach = iota
	ReachPrivate
	ReachCluster
)

type URL struct {
	text  string
	host  string
	reach Reach
}

func (u URL) String() string { return u.text }
func (u URL) Host() string   { return u.host }
func (u URL) Reach() Reach   { return u.reach }
func (u URL) IsZero() bool   { return u.text == "" }
func (u URL) Linkable() bool { return u.text != "" && u.reach != ReachCluster }

func (u URL) WithinCluster() URL {
	if u.IsZero() {
		return u
	}
	u.reach = ReachCluster
	return u
}

func (u URL) Punycode() bool {
	for _, label := range strings.Split(u.host, ".") {
		if strings.HasPrefix(label, "xn--") {
			return true
		}
	}
	return false
}

func New(scheme, host string, port int, path string) (URL, error) {
	if scheme != "http" && scheme != "https" {
		return URL{}, ErrScheme
	}
	shown, reach, err := checkHost(host)
	if err != nil {
		return URL{}, err
	}
	if port < 1 || port > 65535 {
		return URL{}, ErrPort
	}
	if err := checkPath(path); err != nil {
		return URL{}, err
	}
	text := scheme + "://" + shown
	if scheme == "http" && port != 80 || scheme == "https" && port != 443 {
		text += ":" + strconv.Itoa(port)
	}
	if path != "/" {
		text += path
	}
	parsed, err := url.Parse(text)
	if err != nil || parsed.String() != text || parsed.User != nil || parsed.Scheme != scheme || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Hostname() != host {
		return URL{}, ErrCanonical
	}
	return URL{text: text, host: host, reach: reach}, nil
}

func checkHost(host string) (string, Reach, error) {
	if host == "" {
		return "", 0, ErrEmptyHost
	}
	for i := 0; i < len(host); i++ {
		if host[i] >= 0x80 {
			return "", 0, ErrNonASCII
		}
	}
	if strings.Contains(host, "*") {
		return "", 0, ErrWildcard
	}
	if strings.Contains(host, ":") {
		return checkIPv6(host)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		reach, err := addrReach(addr)
		return host, reach, err
	}
	return checkName(host)
}

func checkIPv6(host string) (string, Reach, error) {
	addr, err := netip.ParseAddr(host)
	if err != nil || addr.Zone() != "" {
		return "", 0, ErrHost
	}
	if addr.Is4In6() || addr.String() != host {
		return "", 0, ErrAmbiguous
	}
	reach, err := addrReach(addr)
	return "[" + host + "]", reach, err
}

var (
	reservedPrefixes = prefixes("0.0.0.0/8", "192.0.0.0/24", "240.0.0.0/4", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2002::/16", "2001::/32")
	privatePrefixes  = prefixes("100.64.0.0/10", "198.18.0.0/15")
)

func prefixes(list ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(list))
	for i, p := range list {
		out[i] = netip.MustParsePrefix(p)
	}
	return out
}

func contains(list []netip.Prefix, addr netip.Addr) bool {
	for _, p := range list {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func addrReach(addr netip.Addr) (Reach, error) {
	switch {
	case addr.IsUnspecified(), addr.IsLoopback(), addr.IsLinkLocalUnicast(), addr.IsMulticast(), addr.IsInterfaceLocalMulticast(), contains(reservedPrefixes, addr):
		return 0, ErrLocal
	case addr.IsPrivate(), contains(privatePrefixes, addr):
		return ReachPrivate, nil
	}
	return ReachPublic, nil
}

func checkName(host string) (string, Reach, error) {
	if len(host) > maxHost {
		return "", 0, ErrHost
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if !validLabel(label) {
			return "", 0, ErrHost
		}
	}
	last := labels[len(labels)-1]
	if numericLabel(last) {
		return "", 0, ErrAmbiguous
	}
	if last == "localhost" || last == "localdomain" {
		return "", 0, ErrLocal
	}
	if len(labels) == 1 {
		return "", 0, ErrShort
	}
	switch {
	case last == "svc", strings.HasSuffix(host, ".svc.cluster.local"), strings.HasSuffix(host, ".pod.cluster.local"):
		return host, ReachCluster, nil
	case last == "local", last == "internal", last == "lan", last == "home", last == "corp", strings.HasSuffix(host, ".home.arpa"):
		return host, ReachPrivate, nil
	}
	return host, ReachPublic, nil
}

func validLabel(label string) bool {
	if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

func numericLabel(label string) bool {
	if strings.HasPrefix(label, "0x") {
		return true
	}
	for i := 0; i < len(label); i++ {
		if label[i] < '0' || label[i] > '9' {
			return false
		}
	}
	return true
}

func checkPath(path string) error {
	if path == "" || path[0] != '/' || len(path) > maxPath {
		return ErrPath
	}
	for i := 0; i < len(path); i++ {
		c := path[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '/', c == '-', c == '_', c == '.', c == '~':
		default:
			return ErrPath
		}
	}
	segments := strings.Split(path[1:], "/")
	for i, seg := range segments {
		if seg == "." || seg == ".." || seg == "" && i < len(segments)-1 {
			return ErrPath
		}
	}
	return nil
}
