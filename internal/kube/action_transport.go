package kube

import (
	"errors"
	"net/http"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var errActionRedirect = errors.New("redirect recusado no cliente de acoes")

type singleAttempt struct{ next http.RoundTripper }

func (s singleAttempt) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := s.next.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	if resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
		return nil, errors.Join(errActionRedirect, resp.Body.Close())
	}
	if req.Method != http.MethodGet {
		resp.Header.Del("Retry-After")
	}
	return resp, nil
}

func (s singleAttempt) WrappedRoundTripper() http.RoundTripper { return s.next }

func actionConfig(cfg *rest.Config) *rest.Config {
	once := rest.CopyConfig(cfg)
	once.Wrap(func(rt http.RoundTripper) http.RoundTripper { return singleAttempt{next: rt} })
	return once
}

func newActionClient(cfg *rest.Config) (dynamic.Interface, error) {
	return dynamic.NewForConfig(actionConfig(cfg))
}

func newActionClients(cfg *rest.Config) (kubernetes.Interface, dynamic.Interface, error) {
	auth, err := kubernetes.NewForConfig(actionConfig(cfg))
	if err != nil {
		return nil, nil, err
	}
	client, err := newActionClient(cfg)
	if err != nil {
		return nil, nil, err
	}
	return auth, client, nil
}
