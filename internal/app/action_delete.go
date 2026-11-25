package app

import (
	"fmt"
	"strconv"

	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/ui"
)

const defaultGraceSeconds = 30

var deleteAction = actionSpec{
	action:  guard.Delete,
	label:   "Excluir",
	title:   "Excluir",
	confirm: "Confirmar exclusao",
	success: "Exclusao aceita pelo servidor. O objeto pode permanecer enquanto termina.",
	focus:   2,
	inputs: func(obs guard.Observation) []actionInput {
		grace := int64(defaultGraceSeconds)
		if obs.GraceKnown && obs.GraceSeconds > 0 {
			grace = obs.GraceSeconds
		}
		return []actionInput{
			{label: "Propagacao", value: "Background", choices: []string{"Background", "Foreground"}},
			{label: "Prazo de encerramento (s)", value: strconv.FormatInt(grace, 10)},
		}
	},
	intro: func(obs guard.Observation, t guard.Target) []ui.ActionLine {
		lines := []ui.ActionLine{{Text: "Excluir " + t.Resource + "/" + t.Name + ". Esta acao nao pode ser desfeita pela TUI.", Tone: ui.ActionWarn}}
		if obs.Owner != "" {
			lines = append(lines, ui.ActionLine{Text: "Controlado por " + obs.Owner + ". O controlador pode criar outro objeto."})
		}
		if obs.Terminating {
			lines = append(lines, ui.ActionLine{Text: fmt.Sprintf("O objeto ja esta em encerramento; finalizers observados: %d.", obs.Finalizers), Tone: ui.ActionWarn})
		}
		return lines
	},
	build: func(_ guard.Observation, in []actionInput) (guard.Parameters, string) {
		grace, ok := parseCount(in[1].value, 1)
		if !ok {
			return guard.Parameters{}, "Informe um prazo inteiro de 1 a 2147483647 segundos."
		}
		return guard.Parameters{Propagation: in[0].value, GraceSeconds: int64(grace)}, ""
	},
	effect: func(_ guard.Observation, p guard.Parameters, _ guard.Target) []ui.ActionLine {
		return []ui.ActionLine{{Text: fmt.Sprintf("Propagacao: %s. Prazo de encerramento solicitado: %ds. Finalizers podem adiar a remocao.", p.Propagation, p.GraceSeconds)}}
	},
	observe: func(obs guard.Observation) string {
		if obs.Terminating {
			return fmt.Sprintf("objeto em encerramento (Terminating); finalizers: %d.", obs.Finalizers)
		}
		return "o objeto original continua no servidor, sem encerramento observado."
	},
}
