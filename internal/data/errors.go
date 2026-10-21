package data

import (
	"context"
	"errors"
)

const RedactedFailure = "falha ao conectar; detalhes omitidos por segurança"

type PublicError interface {
	error
	PublicMessage() string
}

type publicError struct{ msg string }

func (e publicError) Error() string { return e.msg }

func (e publicError) PublicMessage() string { return e.msg }

func NewPublicError(msg string) error {
	return publicError{msg: msg}
}

func PublicText(err error) string {
	var pub PublicError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &pub):
		return pub.PublicMessage()
	case errors.Is(err, context.Canceled):
		return "conexão cancelada"
	}
	return RedactedFailure
}
