package kube

import (
	"context"
	"errors"
	"io"
	"net/http"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"time"
)

const resourceResponseLimit = 8 << 20
const resourceObjectLimit = 2 << 20

var errResourceResponseLimit = errors.New("resource response budget exceeded")

type resourceTransport struct {
	base http.RoundTripper
	ctx  context.Context
}

func (t resourceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cancel := func() {}
	stop := func() bool { return false }
	if t.ctx != nil {
		var ctx context.Context
		ctx, cancel = context.WithCancel(request.Context())
		stop = context.AfterFunc(t.ctx, cancel)
		request = request.Clone(ctx)
	}
	release := func() { stop(); cancel() }
	response, err := t.base.RoundTrip(request)
	if err != nil {
		release()
		return nil, err
	}
	watch := request.URL.Query().Get("watch") == "true"
	if !watch && response.ContentLength > resourceResponseLimit {
		_ = response.Body.Close()
		release()
		return nil, errResourceResponseLimit
	}
	response.Body = &resourceBody{ReadCloser: response.Body, watch: watch, release: release}
	return response, nil
}

type resourceBody struct {
	io.ReadCloser
	watch     bool
	release   func()
	bytes     int
	depth     int
	itemBytes int
	inString  bool
	escaped   bool
}

func (b *resourceBody) Read(out []byte) (int, error) {
	n, err := b.ReadCloser.Read(out)
	for i, c := range out[:n] {
		b.bytes++
		if b.depth >= 3 {
			b.itemBytes++
		}
		limit := resourceResponseLimit
		if b.watch {
			limit = resourceObjectLimit
		}
		if b.bytes > limit || b.itemBytes > resourceObjectLimit {
			return i, errResourceResponseLimit
		}
		if b.inString {
			if b.escaped {
				b.escaped = false
			} else if c == '\\' {
				b.escaped = true
			} else if c == '"' {
				b.inString = false
			}
			continue
		}
		switch c {
		case '"':
			b.inString = true
		case '{', '[':
			b.depth++
			if b.depth == 3 {
				b.itemBytes = 1
			}
		case '}', ']':
			b.depth--
			if b.depth < 3 {
				b.itemBytes = 0
			}
			if b.watch && b.depth == 0 {
				b.bytes = 0
			}
		}
	}
	return n, err
}

func newResourceClient(cfg *rest.Config) (dynamic.Interface, error) {
	limited := rest.CopyConfig(cfg)
	limited.Wrap(func(base http.RoundTripper) http.RoundTripper { return resourceTransport{base: base} })
	return dynamic.NewForConfig(limited)
}

func newResourceDiscovery(cfg *rest.Config, ctx context.Context) (discovery.DiscoveryInterface, error) {
	limited := rest.CopyConfig(cfg)
	limited.Timeout = 10 * time.Second
	limited.Wrap(func(base http.RoundTripper) http.RoundTripper { return resourceTransport{base: base, ctx: ctx} })
	return discovery.NewDiscoveryClientForConfig(limited)
}

func (b *resourceBody) Close() error {
	if b.release != nil {
		b.release()
	}
	return b.ReadCloser.Close()
}
