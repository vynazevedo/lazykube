package kube

import (
	"errors"
	"net/http"

	"k8s.io/apimachinery/pkg/types"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	authorizationclient "k8s.io/client-go/kubernetes/typed/authorization/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
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

type freshConnection struct{ cfg *rest.Config }

func (f freshConnection) build() (*http.Transport, error) {
	conf, err := f.cfg.TransportConfig()
	if err != nil {
		return nil, err
	}
	conf.TLS.NextProtos = []string{"http/1.1"}
	tlsConfig, err := transport.TLSConfigFor(conf)
	if err != nil {
		return nil, err
	}
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	base := &http.Transport{
		Proxy:              conf.Proxy,
		TLSClientConfig:    tlsConfig,
		DisableCompression: conf.DisableCompression,
		DisableKeepAlives:  true,
		Protocols:          protocols,
	}
	if conf.DialHolder != nil {
		base.DialContext = conf.DialHolder.Dial
	}
	return utilnet.SetOldTransportDefaults(base), nil
}

func (f freshConnection) RoundTrip(req *http.Request) (*http.Response, error) {
	base, err := f.build()
	if err != nil {
		if req.Body != nil {
			err = errors.Join(err, req.Body.Close())
		}
		return nil, err
	}
	return base.RoundTrip(req)
}

func actionHTTPClient(cfg *rest.Config) (*http.Client, error) {
	base := cfg.Transport
	if base == nil {
		fresh := freshConnection{cfg: cfg}
		if _, err := fresh.build(); err != nil {
			return nil, err
		}
		base = fresh
	}
	wrapped, err := rest.HTTPWrappersForConfig(cfg, base)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: refuseRedirects{next: wrapped}, Timeout: cfg.Timeout}, nil
}

func newActionClient(cfg *rest.Config) (dynamic.Interface, error) {
	own := rest.CopyConfig(cfg)
	httpClient, err := actionHTTPClient(own)
	if err != nil {
		return nil, err
	}
	return actionDynamic(own, httpClient)
}

func actionDynamic(cfg *rest.Config, httpClient *http.Client) (dynamic.Interface, error) {
	conf := dynamic.ConfigFor(cfg)
	conf.GroupVersion = nil
	conf.APIPath = "/"
	client, err := rest.UnversionedRESTClientForConfigAndClient(conf, httpClient)
	if err != nil {
		return nil, err
	}
	return dynamic.New(singleAttempt{client}), nil
}

func newActionClients(cfg *rest.Config) (kubernetes.Interface, dynamic.Interface, error) {
	own := rest.CopyConfig(cfg)
	httpClient, err := actionHTTPClient(own)
	if err != nil {
		return nil, nil, err
	}
	set, err := kubernetes.NewForConfigAndClient(own, httpClient)
	if err != nil {
		return nil, nil, err
	}
	client, err := actionDynamic(own, httpClient)
	if err != nil {
		return nil, nil, err
	}
	auth := authorizationclient.New(singleAttempt{set.AuthorizationV1().RESTClient()})
	return actionClientset{Interface: set, authorization: auth}, client, nil
}
