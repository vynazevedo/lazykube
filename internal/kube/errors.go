package kube

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/vynazevedo/lazykube/internal/data"
)

type ConnectErrorKind int

const (
	ErrorUnknown ConnectErrorKind = iota
	ErrorKubeconfig
	ErrorNoCurrentContext
	ErrorContextMissing
	ErrorClientConfig
	ErrorUnauthorized
	ErrorForbidden
	ErrorExecPlugin
	ErrorUnreachable
	ErrorTLS
	ErrorTimeout
	ErrorCanceled
	ErrorServer
	ErrorGuard
)

type ConnectError struct {
	Kind    ConnectErrorKind
	Context string
	Exec    bool
	cause   error
}

func (e *ConnectError) Unwrap() error {
	return e.cause
}

func (e *ConnectError) Error() string {
	name := fmt.Sprintf("%q", data.Sanitize(e.Context))
	switch e.Kind {
	case ErrorKubeconfig:
		return "kubeconfig ilegível ou inválido"
	case ErrorNoCurrentContext:
		return "o kubeconfig não define current-context; use --context ou escolha um contexto"
	case ErrorContextMissing:
		return "contexto " + name + " não existe no kubeconfig"
	case ErrorClientConfig:
		return "configuração do contexto " + name + " inválida ou incompleta"
	case ErrorUnauthorized:
		if e.Exec {
			return "credenciais do contexto " + name + " recusadas pelo servidor (401); o plugin de autenticação pode ter falhado e sua saída foi omitida"
		}
		return "credenciais do contexto " + name + " recusadas pelo servidor (401)"
	case ErrorForbidden:
		return "acesso negado ao contexto " + name + " (403)"
	case ErrorExecPlugin:
		return "o plugin de autenticação do contexto " + name + " falhou; sua saída foi omitida"
	case ErrorUnreachable:
		return "servidor da API do contexto " + name + " inacessível"
	case ErrorTLS:
		return "falha na verificação TLS do servidor do contexto " + name
	case ErrorTimeout:
		return "tempo esgotado ao conectar ao contexto " + name
	case ErrorCanceled:
		return "conexão cancelada"
	case ErrorServer:
		return "o servidor do contexto " + name + " respondeu com erro"
	case ErrorGuard:
		return "não foi possível preparar a sessão protegida do contexto " + name
	}
	return "falha ao conectar ao contexto " + name
}

func (e *ConnectError) PublicMessage() string {
	return e.Error()
}

var _ data.PublicError = (*ConnectError)(nil)

func connectError(kind ConnectErrorKind, context string, cause error) error {
	return &ConnectError{Kind: kind, Context: context, cause: cause}
}

func publicError(err error, context string, exec bool) error {
	if err == nil {
		return nil
	}
	var known *ConnectError
	if errors.As(err, &known) {
		return known
	}
	return &ConnectError{Kind: classifyConnect(err), Context: context, Exec: exec, cause: err}
}

func classifyConnect(err error) ConnectErrorKind {
	var (
		netErr     net.Error
		opErr      *net.OpError
		dnsErr     *net.DNSError
		unknownCA  x509.UnknownAuthorityError
		invalidCrt x509.CertificateInvalidError
		hostname   x509.HostnameError
		verify     *tls.CertificateVerificationError
		status     apierrors.APIStatus
	)
	switch {
	case errors.Is(err, context.Canceled):
		return ErrorCanceled
	case errors.Is(err, context.DeadlineExceeded), apierrors.IsTimeout(err), apierrors.IsServerTimeout(err):
		return ErrorTimeout
	case apierrors.IsUnauthorized(err):
		return ErrorUnauthorized
	case apierrors.IsForbidden(err):
		return ErrorForbidden
	case strings.Contains(err.Error(), "getting credentials"), strings.Contains(err.Error(), "exec plugin"):
		return ErrorExecPlugin
	case errors.As(err, &unknownCA), errors.As(err, &invalidCrt), errors.As(err, &hostname), errors.As(err, &verify):
		return ErrorTLS
	case errors.As(err, &netErr) && netErr.Timeout():
		return ErrorTimeout
	case errors.As(err, &opErr), errors.As(err, &dnsErr):
		return ErrorUnreachable
	case errors.As(err, &status):
		switch status.Status().Code {
		case http.StatusUnauthorized:
			return ErrorUnauthorized
		case http.StatusForbidden:
			return ErrorForbidden
		}
		return ErrorServer
	}
	return ErrorUnknown
}
