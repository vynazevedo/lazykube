package kube

import (
	"errors"
	"net/http"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	authorizationclient "k8s.io/client-go/kubernetes/typed/authorization/v1"
	"k8s.io/client-go/rest"
)

var errActionRedirect = errors.New("redirect recusado no cliente de acoes")

type refuseRedirects struct{ next http.RoundTripper }

func (s refuseRedirects) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := s.next.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	if resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
		return nil, errors.Join(errActionRedirect, resp.Body.Close())
	}
	return resp, nil
}

func (s refuseRedirects) WrappedRoundTripper() http.RoundTripper { return s.next }

type singleAttempt struct{ rest.Interface }

func (s singleAttempt) Verb(verb string) *rest.Request { return s.Interface.Verb(verb).MaxRetries(0) }
func (s singleAttempt) Get() *rest.Request             { return s.Interface.Get().MaxRetries(0) }
func (s singleAttempt) Post() *rest.Request            { return s.Interface.Post().MaxRetries(0) }
func (s singleAttempt) Put() *rest.Request             { return s.Interface.Put().MaxRetries(0) }
func (s singleAttempt) Delete() *rest.Request          { return s.Interface.Delete().MaxRetries(0) }
func (s singleAttempt) Patch(pt types.PatchType) *rest.Request {
	return s.Interface.Patch(pt).MaxRetries(0)
}

type actionClientset struct {
	kubernetes.Interface
	authorization authorizationclient.AuthorizationV1Interface
}

func (c actionClientset) AuthorizationV1() authorizationclient.AuthorizationV1Interface {
	return c.authorization
}

func actionConfig(cfg *rest.Config) *rest.Config {
	once := rest.CopyConfig(cfg)
	once.Wrap(func(rt http.RoundTripper) http.RoundTripper { return refuseRedirects{next: rt} })
	return once
}

func newActionClient(cfg *rest.Config) (dynamic.Interface, error) {
	once := dynamic.ConfigFor(actionConfig(cfg))
	once.GroupVersion = nil
	once.APIPath = "/"
	client, err := rest.UnversionedRESTClientFor(once)
	if err != nil {
		return nil, err
	}
	return dynamic.New(singleAttempt{client}), nil
}

func newActionClients(cfg *rest.Config) (kubernetes.Interface, dynamic.Interface, error) {
	set, err := kubernetes.NewForConfig(actionConfig(cfg))
	if err != nil {
		return nil, nil, err
	}
	client, err := newActionClient(cfg)
	if err != nil {
		return nil, nil, err
	}
	auth := authorizationclient.New(singleAttempt{set.AuthorizationV1().RESTClient()})
	return actionClientset{Interface: set, authorization: auth}, client, nil
}
