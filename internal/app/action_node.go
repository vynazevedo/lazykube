package app

import (
	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func schedulingText(unschedulable bool) string {
	if unschedulable {
		return "novos agendamentos impedidos"
	}
	return "novos agendamentos permitidos"
}

func schedulingAction(action guard.Action, label, title, confirm, effect string) actionSpec {
	cordon := action == guard.Cordon
	return actionSpec{
		action:  action,
		label:   label,
		title:   title,
		confirm: confirm,
		success: "Configuracao de agendamento aceita pelo servidor.",
		intro: func(obs guard.Observation, _ guard.Target) []ui.ActionLine {
			return []ui.ActionLine{
				{Text: effect},
				{Text: "Antes: " + schedulingText(obs.Unschedulable) + ". Depois: " + schedulingText(cordon) + "."},
			}
		},
		build: func(obs guard.Observation, _ []actionInput) (guard.Parameters, string) {
			if obs.Unschedulable == cordon {
				return guard.Parameters{}, textNoChange
			}
			return guard.Parameters{}, ""
		},
		observe: func(obs guard.Observation) string {
			return schedulingText(obs.Unschedulable) + "."
		},
	}
}

var cordonAction = schedulingAction(guard.Cordon, "Impedir novos agendamentos", "Cordon", "Confirmar cordon",
	"Impedir novos agendamentos neste node. Pods existentes nao serao removidos.")

var uncordonAction = schedulingAction(guard.Uncordon, "Permitir novos agendamentos", "Uncordon", "Confirmar uncordon",
	"Permitir novos agendamentos neste node; taints e demais restricoes continuam valendo.")
