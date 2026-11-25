package app

import (
	"fmt"
	"time"

	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/ui"
)

const actionHint = "Tab foco | Enter controle focado | Esc cancelar"

func (m Model) actionDialog() ui.ActionDialogState {
	a := m.act
	st := ui.ActionDialogState{Title: m.actionTitle(), Lines: m.actionLines(), Error: a.err, Scroll: a.scroll, Hint: actionHint}
	fields, buttons := m.actionFields(), m.actionButtons()
	for i, f := range fields {
		st.Fields = append(st.Fields, ui.ActionField{Label: f.label, Value: f.value, Focused: i == a.focus, Choice: len(f.choices) > 0})
	}
	for i, b := range buttons {
		st.Buttons = append(st.Buttons, ui.ActionButton{Label: b.label, Disabled: b.disabled, Focused: len(fields)+i == a.focus})
	}
	switch a.stage {
	case actionChoosing:
		st.Cursor, st.Hint = a.cursor, "j/k seleciona | Enter escolhe | Esc fecha"
		for _, s := range a.items {
			item := ui.ActionItem{Label: s.label}
			if m.itemBlock() != "" {
				item.Reason = "indisponivel"
			}
			st.Items = append(st.Items, item)
		}
	case actionObserving, actionPreparing, actionReconciling:
		st.Hint = "Esc cancela"
	case actionExecuting:
		st.Hint = "Esc cancela a espera. Cancelar nao desfaz uma requisicao ja enviada."
	case actionResult:
		st.Hint = "Esc fecha | Tab foco | Enter controle focado"
	}
	return st
}

func (m Model) actionTitle() string {
	a := m.act
	name := targetName(a.target)
	switch a.stage {
	case actionChoosing:
		return "Acoes - " + name
	case actionPreview:
		return a.spec.title + " " + name + " - preview validado"
	case actionResult, actionReconciling:
		if a.spec.title == "" {
			return "Acoes - " + name
		}
		return a.spec.title + " " + name + " - resultado"
	}
	return a.spec.title + " " + name
}

func (m Model) actionIdentity(t guard.Target) []ui.ActionLine {
	return []ui.ActionLine{
		{Text: targetScope(m.act.context, t, m.act.tag.session)},
		{Text: "Recurso: " + targetResource(t)},
		{Text: "UID: " + orMissing(t.UID) + " | ResourceVersion: " + orMissing(t.ResourceVersion), Tone: ui.ActionMuted},
	}
}

func orMissing(s string) string {
	if s == "" {
		return "desconhecido"
	}
	return s
}

func (m Model) actionLines() []ui.ActionLine {
	a := m.act
	target := a.target
	if a.plan.Name != "" {
		target = a.plan
	}
	lines := m.actionIdentity(target)
	add := func(tone ui.ActionTone, texts ...string) {
		for _, t := range texts {
			lines = append(lines, ui.ActionLine{Text: t, Tone: tone})
		}
	}
	switch a.stage {
	case actionChoosing:
		if reason := m.itemBlock(); reason != "" {
			add(ui.ActionPlain, "")
			add(ui.ActionWarn, reason)
		}
	case actionObserving:
		add(ui.ActionMuted, "", "Lendo o alvo observado...")
	case actionEditing:
		add(ui.ActionPlain, "")
		lines = append(lines, a.spec.intro(a.obs, a.plan)...)
	case actionPreparing:
		add(ui.ActionMuted, "", "Validando politica, permissoes e alvo. Preparando dry-run...")
	case actionPreview:
		add(ui.ActionPlain, "")
		lines = append(lines, a.spec.intro(a.obs, a.plan)...)
		if a.spec.effect != nil {
			lines = append(lines, a.spec.effect(a.obs, a.params, a.plan)...)
		}
		add(ui.ActionMuted, "Politica: RW por --read-write; contexto nao protegido", accessText(m.actionRequest()))
		if !a.expired {
			add(ui.ActionOK, "Dry-run server-side aprovado.")
			add(ui.ActionPlain, fmt.Sprintf("Preview valido por %ds. Alterar parametros exige nova validacao.", m.previewSeconds()))
			add(ui.ActionMuted, "ID: "+shortID(a.preview.ID)+" | expira: "+a.preview.ExpiresAt.UTC().Format("2006-01-02 15:04:05 UTC"))
		}
		add(ui.ActionPlain, "Nenhuma alteracao real foi enviada.")
	case actionExecuting:
		add(ui.ActionPlain, "", "Enviando e aguardando resposta...")
		if a.waiting {
			add(ui.ActionWarn, textWaitCanceled)
		}
		add(ui.ActionMuted, "Operacao: "+a.opID, textExitWhileSending)
	case actionResult, actionReconciling:
		add(ui.ActionPlain, "")
		lines = append(lines, a.lines...)
		if a.code != "" && a.code != guard.Succeeded {
			add(ui.ActionMuted, "Codigo: "+string(a.code)+" | hora local: "+a.at.Format("15:04:05"))
		}
		if a.opID != "" {
			add(ui.ActionMuted, "Operacao: "+a.opID)
		}
		if a.stage == actionReconciling {
			add(ui.ActionMuted, "Lendo o estado do alvo...")
		}
		lines = append(lines, a.checked...)
	}
	return lines
}

func (m Model) previewSeconds() int {
	return int(max(0, m.act.preview.ExpiresAt.Sub(m.now())+time.Second-1) / time.Second)
}
