package ui

import (
	"fmt"
	"strings"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

type SecurityPageTab int

const (
	SecTabWorkloads SecurityPageTab = iota
	SecTabRBAC
	SecTabNetwork
	SecTabPSS
	SecTabAccepted
	SecTabAudit
	secTabCount
)

var securityTabNames = [secTabCount]string{"Workloads", "RBAC", "Rede e exposição", "Conformidade PSS", "Achados aceitos", "Audit"}

const securityTabsFirstRow = 4

func (t SecurityPageTab) String() string {
	return securityTabNames[t]
}

func (t SecurityPageTab) Next(delta int) SecurityPageTab {
	return SecurityPageTab((int(t) + delta + int(secTabCount)) % int(secTabCount))
}

const (
	SecurityFocusList = iota
	SecurityFocusEvidence
)

type SecurityPageState struct {
	Synthetic   bool
	Namespace   string
	All         data.SecurityAggregate
	NoSystem    data.SecurityAggregate
	NoSystemCut bool
	Alerts      int
	History     []float64
	RuleVersion string
	Tab         SecurityPageTab
	Focus       int
	Cohort      string
	Workloads   []data.WorkloadPosture
	Cursor      int
	Rules       []data.WorkloadRule
	Scroll      int
}

func (st SecurityPageState) Selected() *data.WorkloadPosture {
	if st.Cursor < 0 || st.Cursor >= len(st.Workloads) {
		return nil
	}
	return &st.Workloads[st.Cursor]
}

func gradeColor(s data.PostureScore) theme.RGB {
	if !s.Available {
		return theme.Muted
	}
	switch s.Grade {
	case "A", "B":
		return theme.OK
	case "C", "D":
		return theme.Warn
	}
	return theme.Err
}

func pageScore(s data.PostureScore) draw.Span {
	if !s.Available {
		return muted("sem amostra")
	}
	label := fmt.Sprintf("%d %s", s.Value, s.Grade)
	if s.Partial {
		label += " parcial"
	}
	return colored(label, gradeColor(s))
}

func cohortLine(label string, a data.SecurityAggregate) draw.Line {
	return draw.Line{muted(label + ": Segurança "), pageScore(a.Score), muted(" | Higiene "), pageScore(a.Hygiene)}
}

func pageCoverageText(c data.Coverage) string {
	text := fmt.Sprintf("Cobertura %d/%d pods (%s)", c.Evaluated, c.Total, c.State)
	if c.Reason != "" {
		text += ": " + draw.Sanitize(c.Reason)
	}
	return text
}

func SecuritySummaryLines(st SecurityPageState) []draw.Line {
	scope := "Cluster"
	if st.Namespace != "" {
		scope = "Namespace " + draw.Sanitize(st.Namespace)
	}
	first := append(draw.Line{text(scope + " | ")}, cohortLine("Todos", st.All)...)
	if !st.NoSystemCut {
		first = append(append(first, faint(" | ")), cohortLine("Sem sistema", st.NoSystem)...)
	}
	d := st.All.Distribution
	second := draw.Line{muted("Segurança: mediana "), text("-"), muted(" | pior decil "), text("-")}
	if st.All.Score.Available {
		second = draw.Line{muted("Segurança: mediana "), text(fmt.Sprint(d.Median)), muted(" | pior decil "), text(fmt.Sprint(d.WorstDecile)), muted(" | A..F ")}
		for i, g := range "ABCDEF" {
			second = append(second, text(fmt.Sprintf("%c%d ", g, d.Grades[i])))
		}
	}
	third := draw.Line{muted("High novos "), text(fmt.Sprint(st.Alerts)), faint(" | "), muted(pageCoverageText(st.All.Score.Coverage)), faint(" | "), colored("Nota não garante segurança", theme.Warn)}
	trend := "pausada até haver cobertura avaliável"
	if st.All.Score.Available && len(st.History) > 0 {
		trend = draw.Sparkline(st.History, 100) + " (revisões da sessão)"
	}
	fourth := draw.Line{muted("Score v" + data.SecurityScoreVersion + " | regras v" + draw.Sanitize(st.RuleVersion) + " | tendência "), text(trend)}
	return []draw.Line{first, second, third, fourth}
}

func SecuritySummary(r draw.Region, st SecurityPageState, ctx Ctx) {
	inner := Panel(r, "Postura de segurança", false, ctx).Inset(1, 0, 1, 0)
	for y, l := range SecuritySummaryLines(st) {
		if y >= inner.H {
			break
		}
		inner.Line(0, y, l.Truncate(inner.W))
	}
}

type tabSpot struct {
	tab     SecurityPageTab
	row, x  int
	width   int
	visible bool
}

func securityTabSpots(w int) []tabSpot {
	out := make([]tabSpot, 0, secTabCount)
	row, x := 0, 0
	for t := SecurityPageTab(0); t < secTabCount; t++ {
		label := "[" + t.String() + "]"
		width := draw.Width(label)
		if t == securityTabsFirstRow || (x > 0 && x+width > w) {
			row, x = row+1, 0
		}
		out = append(out, tabSpot{tab: t, row: row, x: x, width: width, visible: row < 2})
		x += width + 1
	}
	return out
}

func SecurityTabAt(w, x, y int) (SecurityPageTab, bool) {
	for _, s := range securityTabSpots(w) {
		if s.visible && y == s.row && x >= s.x && x < s.x+s.width {
			return s.tab, true
		}
	}
	return 0, false
}

func SecurityTabs(r draw.Region, active SecurityPageTab, ctx Ctx) {
	r.Fill(theme.BgApp)
	for _, s := range securityTabSpots(r.W - 2) {
		if !s.visible {
			continue
		}
		st := draw.Fg(theme.Muted)
		if s.tab == active {
			st = draw.Fg(ctx.Pal.A).WithBg(theme.BorderFaint).WithBold()
		}
		r.Text(1+s.x, s.row, "["+s.tab.String()+"]", st)
	}
}

func SecurityListRows(panelH int) int {
	return max(0, panelH-6)
}

func workloadRow(w data.WorkloadPosture, width int) draw.Line {
	a := w.Aggregate
	right := draw.Line{text(fmt.Sprintf("%4d ", len(w.Pods))), pageCell(a.Score, 4), text(" "), pageCell(a.Hygiene, 4), text(fmt.Sprintf(" %3d", a.High))}
	name := draw.Line{muted(kindAbbrev(w) + " "), text(draw.Sanitize(w.Name)), faint(" " + draw.Sanitize(w.Key.Namespace))}.Truncate(max(1, width-right.Width()-1))
	pad := max(1, width-name.Width()-right.Width())
	return append(append(name, text(strings.Repeat(" ", pad))), right...)
}

func kindAbbrev(w data.WorkloadPosture) string {
	if w.Resolution == data.OwnerStandalone || w.Resolution == data.OwnerUnresolved {
		return "pod"
	}
	switch w.Key.Kind {
	case "ReplicaSet":
		return "rs"
	case "StatefulSet":
		return "sts"
	case "DaemonSet":
		return "ds"
	case "Job":
		return "job"
	case "ReplicationController":
		return "rc"
	}
	return draw.Truncate(strings.ToLower(draw.Sanitize(w.Key.Kind)), 8)
}

func pageCell(s data.PostureScore, width int) draw.Span {
	if !s.Available {
		return muted(draw.PadLeft("-", width))
	}
	label := fmt.Sprint(s.Value)
	if s.Partial {
		label += "p"
	}
	return colored(draw.PadLeft(label, width), gradeColor(s))
}

func resolutionText(w data.WorkloadPosture) string {
	switch w.Resolution {
	case data.OwnerDirect:
		return "Owner: " + w.Key.Kind + " por UID (ownerReference controller)"
	case data.OwnerExternal:
		return "Owner: controlador não reconhecido " + draw.Sanitize(w.APIVersion)
	case data.OwnerUnresolved:
		return "Owner não resolvido: referência sem identidade válida"
	}
	return "Pod sem controlador (standalone)"
}

func SecurityWorkloadsPanel(r draw.Region, st SecurityPageState, ctx Ctx) {
	inner := Panel(r, fmt.Sprintf("Workloads (%d) | %s", len(st.Workloads), st.Cohort), st.Focus == SecurityFocusList, ctx).Inset(1, 0, 1, 0)
	if inner.Empty() {
		return
	}
	inner.Line(0, 0, draw.Line{muted(draw.PadRight("WORKLOAD", max(1, inner.W-22))), muted("PODS  SEG  HIG   H")}.Truncate(inner.W))
	rows := SecurityListRows(r.H)
	if len(st.Workloads) == 0 {
		inner.Line(0, 1, draw.Line{muted("Sem amostra avaliável.")})
	}
	offset := ScrollOffset(st.Cursor, len(st.Workloads), rows)
	for row := 0; row < rows && offset+row < len(st.Workloads); row++ {
		i := offset + row
		if i == st.Cursor {
			inner.Sub(-1, 1+row, inner.W+2, 1).FillBg(SelectionBg(ctx.Pal, selectionMix))
		}
		inner.Line(0, 1+row, workloadRow(st.Workloads[i], inner.W))
	}
	foot := []draw.Line{{faint("Réplicas observadas, não desejadas")}, {faint("Deployment acima do ReplicaSet ainda não resolvido")}}
	if w := st.Selected(); w != nil {
		foot = append([]draw.Line{{muted(resolutionText(*w))}}, foot...)
	}
	for i, l := range foot {
		y := inner.H - len(foot) + i
		if y > rows {
			inner.Line(0, y, l.Truncate(inner.W))
		}
	}
}

func assessmentLabel(s data.AssessmentState) string {
	switch s {
	case data.AssessmentUnsafe:
		return "Unsafe"
	case data.AssessmentUnspecified:
		return "Unspecified"
	case data.AssessmentUnknown:
		return "Unknown"
	}
	return string(s)
}

func severityColor(s data.Severity) theme.RGB {
	switch s {
	case data.SeverityHigh:
		return theme.Err
	case data.SeverityMedium:
		return theme.Warn
	}
	return theme.Muted
}

func SecurityEvidenceLines(st SecurityPageState, width int) []draw.Line {
	w := st.Selected()
	if w == nil {
		return []draw.Line{{muted("Selecione um workload para ver evidências.")}}
	}
	width = max(1, width)
	var out []draw.Line
	wrap := func(s string, span func(string) draw.Span) {
		for _, l := range draw.Wrap(s, width) {
			out = append(out, draw.Line{span(l)})
		}
	}
	a := w.Aggregate
	out = append(out, draw.Line{bold(draw.Sanitize(w.Key.Namespace+"/"+w.Label()), theme.Text)})
	wrap(resolutionText(*w), muted)
	out = append(out, draw.Line{muted(fmt.Sprintf("Pods observados: %d | Segurança ", len(w.Pods))), pageScore(a.Score), muted(" | Higiene "), pageScore(a.Hygiene)})
	wrap(pageCoverageText(a.Score.Coverage), muted)
	if len(st.Rules) == 0 {
		wrap("Nenhum achado nas regras locais avaliadas; ausência de achados não prova segurança.", muted)
		return out
	}
	for _, r := range st.Rules {
		f := r.Evidence
		container := f.Container
		if container == "" {
			container = "pod"
		}
		out = append(out, draw.Line{})
		out = append(out, draw.Line{colored("["+string(r.Severity)+"] ", severityColor(r.Severity)), bold(draw.Sanitize(r.Rule), theme.Text), text(": " + assessmentLabel(r.State) + ", container " + draw.Sanitize(container))})
		wrap(draw.Sanitize(f.Field), text)
		wrap("Valor observado: "+draw.Sanitize(f.Evidence)+" | regra versão "+draw.Sanitize(r.Version), muted)
		wrap(fmt.Sprintf("Origem: %s; spec observado, não runtime | %d/%d pods", draw.Sanitize(f.Origin), r.Pods, len(w.Pods)), muted)
		if f.Explanation != "" || f.Recommendation != "" {
			wrap(draw.Sanitize(strings.TrimSuffix(f.Explanation, ".")+". "+f.Recommendation), text)
		}
		if r.State == data.AssessmentUnknown {
			continue
		}
		rem := data.RemediationFor(r, *w)
		wrap("Alvo: "+draw.Sanitize(rem.Target), muted)
		if len(rem.YAML) > 0 {
			out = append(out, draw.Line{faint("Sugestão YAML; revise compatibilidade antes de aplicar.")})
			for _, l := range rem.YAML {
				out = append(out, draw.Line{colored("  "+draw.Sanitize(l), theme.TextSoft)}.Truncate(width))
			}
		}
		if rem.Caution != "" {
			wrap(rem.Caution, faint)
		}
	}
	out = append(out, draw.Line{})
	wrap("Aplicação pela TUI indisponível nesta versão; sugestão somente.", faint)
	return out
}

func SecurityEvidenceRows(panelH int) int {
	return max(0, panelH-2)
}

func SecurityEvidencePanel(r draw.Region, st SecurityPageState, ctx Ctx) {
	inner := Panel(r, "Evidência e remediação", st.Focus == SecurityFocusEvidence, ctx).Inset(1, 0, 1, 0)
	if inner.Empty() {
		return
	}
	lines := SecurityEvidenceLines(st, inner.W)
	scroll := max(0, min(st.Scroll, len(lines)-inner.H))
	for y := 0; y < inner.H && scroll+y < len(lines); y++ {
		inner.Line(0, y, lines[scroll+y].Truncate(inner.W))
	}
}

func SecurityPendingLines(t SecurityPageTab) []string {
	waiting := "Aguardando projetor do catálogo de recursos; nenhuma leitura foi feita para esta aba."
	switch t {
	case SecTabRBAC:
		return []string{"Concessões RBAC observadas; não são permissões efetivas.", waiting, "Nenhum Role, RoleBinding, ClusterRole ou ClusterRoleBinding foi lido."}
	case SecTabNetwork:
		return []string{"Rede e exposição: alcance público desconhecido.", waiting, "Nenhum Service, Ingress ou NetworkPolicy foi lido."}
	case SecTabPSS:
		return []string{"PSS v1.34: verificação local do spec; não é admissão.", "Avaliação PSS ainda indisponível; a nota de segurança não substitui a conformidade.", "Labels pod-security.kubernetes.io dos namespaces não foram lidos."}
	case SecTabAccepted:
		return []string{"Aceite de risco local ainda indisponível.", "A reconhece alertas da sessão; não aceita risco nem remove evidência."}
	case SecTabAudit:
		return []string{"Audit local indisponível nesta versão; nenhum registro foi carregado."}
	}
	return nil
}

func SecurityPendingPanel(r draw.Region, t SecurityPageTab, ctx Ctx) {
	inner := Panel(r, t.String(), false, ctx).Inset(1, 0, 1, 0)
	y := 0
	for _, s := range SecurityPendingLines(t) {
		for _, l := range draw.Wrap(s, max(1, inner.W)) {
			if y >= inner.H {
				return
			}
			inner.Line(0, y, draw.Line{text(l)})
			y++
		}
	}
}

func SecurityNotes(r draw.Region, st SecurityPageState) {
	r.Fill(theme.BgApp)
	lines := []draw.Line{
		{faint("PSS v1.34: verificação local do spec; não é admissão")},
		{faint("RBAC: concessões observadas, não permissão efetiva. Rede: alcance público desconhecido.")},
	}
	if st.Synthetic {
		lines = append(lines, draw.Line{colored("[DEMO] dados sintéticos", theme.Warn)})
	}
	for y, l := range lines {
		if y >= r.H {
			break
		}
		r.Line(1, y, l.Truncate(max(0, r.W-2)))
	}
}

func SecurityHelp() []KeyHelp {
	return []KeyHelp{
		{"↑↓", "move"},
		{"[ ]", "tab"},
		{"tab", "panel"},
		{"H", "cohort"},
		{"esc", "back"},
		{"m", "menu"},
		{"?", "help"},
		{"q", "quit"},
	}
}

func FullHelpSecurity() []KeyHelp {
	return []KeyHelp{
		{"↑/↓ k/j", "move workload / scroll evidence"},
		{"tab shift+tab", "list / evidence panel"},
		{"enter", "focus evidence"},
		{"[ ]", "previous / next Security tab"},
		{"H", "list cohort: all / without system"},
		{"pgup/pgdn", "scroll evidence"},
		{"home/end", "first / last"},
		{"esc", "back"},
		{"m", "screens menu ← → enter"},
		{"2", "Dashboard screen"},
		{"3", "Workloads screen"},
		{"4", "Nodes screen"},
		{"5", "Security screen"},
		{"6", "Events screen"},
		{"c n", "switch context / namespace"},
		{"A", "ack all Security alerts"},
		{"p", "cycle palette"},
		{"ctrl+p", "pause animations"},
		{"?", "toggle help"},
		{"q ctrl+c", "quit"},
	}
}
