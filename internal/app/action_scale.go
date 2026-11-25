package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/ui"
)

var scaleAction = actionSpec{
	action:  guard.Scale,
	label:   "Escalar",
	title:   "Escalar",
	confirm: "Confirmar escala",
	success: "Escala aceita pelo servidor. Aguardando convergencia das replicas.",
	inputs: func(guard.Observation) []actionInput {
		return []actionInput{{label: "Replicas desejadas"}}
	},
	intro: func(obs guard.Observation, _ guard.Target) []ui.ActionLine {
		return []ui.ActionLine{{Text: fmt.Sprintf("Replicas atuais: %d", obs.Replicas)}}
	},
	build: func(obs guard.Observation, in []actionInput) (guard.Parameters, string) {
		n, ok := parseCount(in[0].value, 0)
		if !ok {
			return guard.Parameters{}, "Informe um inteiro de 0 a 2147483647."
		}
		if obs.Replicas == n {
			return guard.Parameters{}, "Nenhuma alteracao de replicas."
		}
		return guard.Parameters{Replicas: n}, ""
	},
	effect: func(obs guard.Observation, p guard.Parameters, t guard.Target) []ui.ActionLine {
		lines := []ui.ActionLine{
			{Text: fmt.Sprintf("Replicas desejadas: %d", p.Replicas)},
			{Text: fmt.Sprintf("Alterar replicas de %d para %d. A convergencia sera observada apos a resposta.", obs.Replicas, p.Replicas)},
		}
		if p.Replicas == 0 {
			lines = append(lines, ui.ActionLine{Text: "Escalar para zero pode interromper a carga. Digite exatamente " + t.Name + " para confirmar.", Tone: ui.ActionWarn})
		}
		return lines
	},
	observe: func(obs guard.Observation) string {
		return fmt.Sprintf("replicas desejadas %d, replicas observadas %d.", obs.Replicas, obs.ObservedReplicas)
	},
}

func parseCount(value string, low int32) (int32, bool) {
	if value == "" || strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(value, 10, 32)
	return int32(n), err == nil && int32(n) >= low
}
