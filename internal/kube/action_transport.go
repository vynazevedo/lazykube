package kube

import (
	"net/http"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

type singleAttempt struct{ next http.RoundTripper }

func (s singleAttempt) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := s.next.RoundTrip(req)
	if resp != nil && req.Method != http.MethodGet {
		resp.Header.Del("Retry-After")
	}
	return resp, err
}

func (s singleAttempt) WrappedRoundTripper() http.RoundTripper { return s.next }

func newActionClient(cfg *rest.Config) (dynamic.Interface, error) {
	once := rest.CopyConfig(cfg)
	once.Wrap(func(rt http.RoundTripper) http.RoundTripper { return singleAttempt{next: rt} })
	return dynamic.NewForConfig(once)
}
