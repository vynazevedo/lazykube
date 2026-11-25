package app

import (
	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/ui"
)

var restartAction = actionSpec{
	action:  guard.Restart,
	label:   "Reiniciar rollout",
	title:   "Reiniciar rollout de",
	confirm: "Confirmar restart",
	success: "Restart solicitado ao servidor. Acompanhe o rollout.",
	intro: func(obs guard.Observation, _ guard.Target) []ui.ActionLine {
		lines := []ui.ActionLine{{Text: "Atualizar a anotacao de restart do template. A substituicao dos pods depende da estrategia do controlador."}}
		if obs.Strategy == "OnDelete" {
			lines = append(lines, ui.ActionLine{Text: "Estrategia OnDelete: atualizar o template nao substitui pods automaticamente.", Tone: ui.ActionWarn})
		}
		return lines
	},
	build: func(guard.Observation, []actionInput) (guard.Parameters, string) {
		return guard.Parameters{}, ""
	},
	observe: func(guard.Observation) string {
		return "o objeto original continua no servidor; progresso do rollout desconhecido por esta leitura."
	},
}
