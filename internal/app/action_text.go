package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/ui"
)

const (
	textNoAction         = "Nenhuma acao disponivel para este alvo."
	textNoSource         = "Acoes indisponiveis nesta fonte."
	textInvalidConfig    = "Configuracao de politica invalida. Acoes bloqueadas; leitura preservada."
	textProtected        = "Contexto protegido. Acoes bloqueadas mesmo com --read-write."
	textReadOnly         = "Somente leitura. --read-write habilita tentativas de acao apenas nesta execucao; a politica do contexto continua prevalecendo."
	textAuditBlocked     = "Falha na auditoria. Novas acoes bloqueadas. Consulte o estado do alvo; nao repita automaticamente."
	textNoIdentity       = "Atualize o alvo para obter UID e resourceVersion."
	textSelection        = "A selecao mudou. Acao cancelada antes do envio real."
	textCanceled         = "Acao cancelada antes do envio real."
	textPrepareTimeout   = "Tempo de validacao esgotado. A alteracao real nao foi enviada."
	textExpired          = "Preview expirado. Gere um novo preview."
	textNameMismatch     = "O nome digitado nao corresponde ao alvo."
	textUnknown          = "Resultado desconhecido. A requisicao pode ter sido aplicada. Verifique o alvo antes de qualquer nova acao."
	textSucceededAudit   = "Servidor confirmou a acao, mas o registro final de auditoria falhou. Novas acoes bloqueadas."
	textFinalAudit       = "O registro final de auditoria falhou. Novas acoes bloqueadas."
	textNoChange         = "Nenhuma alteracao necessaria."
	textNotAuthorship    = "A leitura nao altera o resultado registrado: outro operador pode ter produzido o mesmo estado."
	textUIDGone          = "O UID original nao existe mais neste nome: objeto removido ou recriado. Nenhuma acao segue o objeto novo."
	textExitWhileSending = "Sair com ctrl+c nao desfaz a operacao: o resultado fica por conferir no alvo e no audit."
	textWaitCanceled     = "Cancelamento da espera solicitado. Cancelar nao desfaz uma requisicao ja enviada."
)

func reasonOf(err error) guard.Reason {
	var r guard.Reason
	if errors.As(err, &r) {
		return r
	}
	return guard.Unknown
}

func reasonText(r guard.Reason) string {
	switch r {
	case guard.ReadOnly:
		return "Acao bloqueada: somente leitura. " + textReadOnly
	case guard.InvalidConfig:
		return textInvalidConfig
	case guard.Denied:
		return "Permissao negada para esta acao neste alvo."
	case guard.Unavailable:
		return "Nao foi possivel validar a acao. Nenhuma nova tentativa sera feita automaticamente."
	case guard.Unsupported:
		return "Acao indisponivel para este recurso ou transporte."
	case guard.Invalid:
		return "Parametros ou identidade invalidos. Revise o formulario e gere novo preview."
	case guard.Stale:
		return "O alvo mudou ou nao esta mais disponivel. Atualize os dados e gere novo preview."
	case guard.DryRunFailed:
		return "Dry-run server-side falhou. A alteracao real nao foi enviada."
	case guard.ConfirmationRequired:
		return "Confirmacao nao aceita. Gere novo preview antes de confirmar novamente."
	case guard.Canceled:
		return textCanceled
	case guard.AuditFailed:
		return textAuditBlocked
	case guard.Unchanged:
		return textNoChange
	}
	return textUnknown
}

func prepareText(err error, timedOut bool) string {
	r := reasonOf(err)
	if timedOut && (r == guard.Canceled || r == guard.Unavailable) {
		return textPrepareTimeout
	}
	if r == guard.Unknown {
		r = guard.Unavailable
	}
	return reasonText(r)
}

func resultLines(res guard.Result, err error, success string) []ui.ActionLine {
	reason := reasonOf(err)
	auditFailed := err != nil && reason == guard.AuditFailed
	switch {
	case res.Outcome == guard.Succeeded && auditFailed:
		return []ui.ActionLine{{Text: textSucceededAudit, Tone: ui.ActionWarn}}
	case res.Outcome == guard.Succeeded && err == nil:
		return []ui.ActionLine{{Text: success, Tone: ui.ActionOK}}
	case res.Outcome == guard.Succeeded:
		return []ui.ActionLine{{Text: textUnknown, Tone: ui.ActionErr}}
	case res.Outcome == "" && err == nil:
		return []ui.ActionLine{{Text: textUnknown, Tone: ui.ActionErr}}
	case res.Outcome == "":
		return []ui.ActionLine{{Text: reasonText(reason), Tone: ui.ActionErr}}
	}
	lines := []ui.ActionLine{{Text: reasonText(res.Outcome), Tone: ui.ActionErr}}
	if auditFailed {
		lines = append(lines, ui.ActionLine{Text: textFinalAudit, Tone: ui.ActionErr})
	}
	return lines
}

var kindNames = map[string]string{
	"pods": "pod", "deployments": "deployment", "statefulsets": "statefulset",
	"daemonsets": "daemonset", "jobs": "job", "cronjobs": "cronjob", "nodes": "node",
}

func targetName(t guard.Target) string {
	kind := kindNames[t.Resource]
	if kind == "" {
		kind = t.Resource
	}
	return kind + "/" + t.Name
}

func targetResource(t guard.Target) string {
	gv := t.Version
	if t.Group != "" {
		gv = t.Group + "/" + t.Version
	}
	return gv + " " + t.Resource + "/" + t.Name
}

func targetScope(context string, t guard.Target, generation uint64) string {
	ns := "Namespace: " + t.Namespace
	if guard.ClusterScoped(t) {
		ns = "Escopo: cluster"
	}
	return fmt.Sprintf("Contexto: %s | %s | Geracao: %d", context, ns, generation)
}

func accessText(r guard.Request) string {
	var parts []string
	for _, a := range guard.Requirements(r) {
		res := a.Resource
		if a.Subresource != "" {
			res += "/" + a.Subresource
		}
		parts = append(parts, a.Verb+" "+res)
	}
	return "SSAR: " + strings.Join(parts, " e ")
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
