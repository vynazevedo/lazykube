package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/insight"
	"github.com/vynazevedo/lazykube/internal/theme"
)

const RadarListTop = 1

type RadarState struct {
	Available bool
	Error     string
	Have      bool
	Snapshot  insight.SignalSnapshot
	Rows      []insight.Signal
	Cursor    int
	Scroll    int
	Gone      bool
	Selected  *insight.Signal
	PodOpen   bool
}

type RadarCounts struct {
	High, Warning, Recovering, Candidates, Suspended, Resolved, Normal int
}

func CountSignals(signals []insight.Signal) RadarCounts {
	var c RadarCounts
	for _, s := range signals {
		switch s.State {
		case insight.SignalActive:
			if s.Severity == insight.SignalHigh {
				c.High++
			} else {
				c.Warning++
			}
		case insight.SignalRecovering:
			c.Recovering++
		case insight.SignalCandidate:
			c.Candidates++
		case insight.SignalSuspended:
			c.Suspended++
		case insight.SignalResolved:
			c.Resolved++
		case insight.SignalNormal:
			c.Normal++
		}
	}
	return c
}

func RuleLabel(r insight.Rule) string {
	switch r {
	case insight.RuleRestarts:
		return "Restarts"
	case insight.RuleCrashLoop:
		return "CrashLoop"
	case insight.RuleOOM:
		return "OOMKilled"
	case insight.RuleImagePull:
		return "ImagePull"
	case insight.RulePending:
		return "Pending"
	case insight.RuleEvicted:
		return "Evicted"
	}
	return string(r)
}

func severityLabel(s insight.SignalSeverity) (string, theme.RGB) {
	switch s {
	case insight.SignalHigh:
		return "High", theme.Err
	case insight.SignalWarning:
		return "Warning", theme.Warn
	}
	return "Info", theme.Muted
}

func signalStateLabel(s insight.SignalState) string {
	switch s {
	case insight.SignalActive:
		return "ativo"
	case insight.SignalRecovering:
		return "recuperando"
	case insight.SignalCandidate:
		return "observando"
	case insight.SignalSuspended:
		return "suspenso"
	case insight.SignalResolved:
		return "resolvido"
	}
	return "normal"
}

func coverageLabel(c insight.Coverage) (string, theme.RGB) {
	switch c.State {
	case insight.CoverageComplete:
		return "completa", theme.OK
	case insight.CoverageCollecting:
		return "coletando", theme.Muted
	case insight.CoveragePartial:
		return "parcial", theme.Warn
	case insight.CoverageDenied:
		return "negada", theme.Err
	case insight.CoverageStale:
		return "desatualizada", theme.Warn
	case insight.CoverageDisabled:
		return "desativada", theme.Muted
	}
	return "indisponível", theme.Muted
}

func suspensionText(r insight.SuspensionReason) string {
	switch r {
	case insight.SuspensionSourceCoverage:
		return "cobertura da fonte incompleta"
	case insight.SuspensionRevisionGap:
		return "revisões de inventário puladas; nova linha de base"
	case insight.SuspensionMissingStatus:
		return "status do container ausente"
	case insight.SuspensionCounterReset:
		return "contador de restarts reiniciado"
	case insight.SuspensionInvalidCounter:
		return "contador de restarts inválido"
	case insight.SuspensionInvalidTimestamp:
		return "horário da fonte inválido ou no futuro"
	case insight.SuspensionBufferLimit:
		return "limite de deltas de restart excedido"
	}
	return ""
}

func reasonLabel(r insight.Reason) string {
	switch r {
	case insight.ReasonCrashLoopBackOff:
		return "CrashLoopBackOff"
	case insight.ReasonOOMKilled:
		return "OOMKilled"
	case insight.ReasonImagePullBackOff:
		return "ImagePullBackOff"
	case insight.ReasonErrImagePull:
		return "ErrImagePull"
	case insight.ReasonContainerCreating:
		return "ContainerCreating"
	case insight.ReasonPodInitializing:
		return "PodInitializing"
	case insight.ReasonEvicted:
		return "Evicted"
	case insight.ReasonNodeLost:
		return "NodeLost"
	case insight.ReasonCompleted:
		return "Completed"
	case insight.ReasonOther:
		return "outro"
	}
	return "não informado"
}

func clockText(t time.Time) string {
	if t.IsZero() {
		return "não informado"
	}
	return t.Format("15:04:05")
}

func durationText(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func signalTarget(s insight.Signal) string {
	t := s.Entity.Name
	if s.Container != "" {
		t += "/" + s.Container
	}
	return draw.Sanitize(t)
}

func RadarSummaryLines(st RadarState) []draw.Line {
	switch {
	case st.Error != "":
		return []draw.Line{{colored("Radar indisponível: "+draw.Sanitize(st.Error), theme.Err)}}
	case !st.Available:
		return []draw.Line{
			{colored("Radar indisponível: esta fonte não publica observações tipadas.", theme.Warn)},
			{faint("Sem observações não há conclusão; isso não significa cluster saudável.")},
		}
	case !st.Have:
		return []draw.Line{{muted("Coletando baseline; ainda sem conclusão.")}}
	}
	c := CountSignals(st.Snapshot.Signals)
	counts := draw.Line{
		muted("Sinais ativos: "), bold(fmt.Sprint(c.High+c.Warning), theme.Text),
		muted(" (High "), colored(fmt.Sprint(c.High), theme.Err), muted(", Warning "), colored(fmt.Sprint(c.Warning), theme.Warn), muted(")"),
		faint(" | "), muted(fmt.Sprintf("recuperando %d", c.Recovering)),
		faint(" | "), muted(fmt.Sprintf("observando %d", c.Candidates)),
		faint(" | "), muted(fmt.Sprintf("suspensos %d", c.Suspended)),
		faint(" | "), muted(fmt.Sprintf("resolvidos %d", c.Resolved)),
		faint(" | "), muted(fmt.Sprintf("normais %d", c.Normal)),
	}
	if st.Snapshot.OmittedSignals > 0 {
		counts = append(counts, faint(" | "), colored(fmt.Sprintf("omitidos %d de %d", st.Snapshot.OmittedSignals, st.Snapshot.TotalSignals), theme.Warn))
	}
	return []draw.Line{
		counts,
		{muted("Saúde: "), text("indisponível"), faint(" - o motor ainda não publica a fórmula; nenhum número é estimado aqui.")},
		{faint("Cada linha é um sinal por pod/container, não um incidente correlacionado. Ack e silêncio ainda não existem.")},
	}
}

func RadarSummary(r draw.Region, st RadarState, ctx Ctx) {
	inner := Panel(r, "Radar", false, ctx).Inset(1, 0, 1, 0)
	for y, l := range RadarSummaryLines(st) {
		if y >= inner.H {
			break
		}
		inner.Line(0, y, l.Truncate(inner.W))
	}
}

func RadarListRows(panelH int) int {
	return max(0, panelH-2-RadarListTop-1)
}

type radarColumns struct{ sev, rule, since int }

func radarListColumns(w int) radarColumns {
	c := radarColumns{sev: 4, rule: 9}
	if w >= radarSinceMinWidth {
		c.since = 5
	}
	return c
}

const radarSinceMinWidth = 40

func (c radarColumns) target(w int) int {
	gaps := 2
	if c.since > 0 {
		gaps = 3
	}
	return max(1, w-2-c.sev-c.rule-c.since-gaps)
}

func radarRow(c radarColumns, w int, s insight.Signal, style func(theme.RGB) draw.Style) draw.Line {
	sev, color := rowStateLabel(s)
	target := c.target(w)
	line := draw.Line{
		draw.S("  ", style(theme.Text)),
		draw.S(draw.PadRight(sev, c.sev), style(color)), draw.S(" ", style(theme.Text)),
		draw.S(draw.PadRight(RuleLabel(s.Rule), c.rule), style(theme.Text)), draw.S(" ", style(theme.Text)),
		draw.S(draw.PadRight(draw.Truncate(signalTarget(s), target), target), style(theme.Text)),
	}
	if c.since > 0 {
		line = append(line, draw.S(" ", style(theme.Text)), draw.S(draw.PadLeft(shortClock(s.FirstObservedAt), c.since), style(theme.Muted)))
	}
	return line
}

func rowStateLabel(s insight.Signal) (string, theme.RGB) {
	switch s.State {
	case insight.SignalActive:
		if s.Severity == insight.SignalHigh {
			return "High", theme.Err
		}
		return "Warn", theme.Warn
	case insight.SignalRecovering:
		return "rec.", theme.Muted
	case insight.SignalCandidate:
		return "obs.", theme.Muted
	case insight.SignalSuspended:
		return "susp", theme.Muted
	case insight.SignalResolved:
		return "res.", theme.Muted
	}
	return "-", theme.Muted
}

func shortClock(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("15:04")
}

func RadarList(r draw.Region, st RadarState, ctx Ctx) {
	inner := Panel(r, "Sinais · focus", true, ctx).Inset(1, 0, 1, 0)
	if inner.Empty() {
		return
	}
	cols := radarListColumns(inner.W)
	header := "  " + draw.PadRight("EST", cols.sev) + " " + draw.PadRight("SINAL", cols.rule) + " " + draw.PadRight("POD/CONTAINER", cols.target(inner.W))
	if cols.since > 0 {
		header += " " + draw.PadLeft("DESDE", cols.since)
	}
	inner.Line(0, 0, draw.Line{muted(header)}.Truncate(inner.W))
	rows := RadarListRows(r.H)
	body := inner.Sub(0, RadarListTop, inner.W, rows)
	coverage, _ := coverageLabel(st.Snapshot.Coverage)
	switch {
	case !st.Available || st.Error != "":
		centeredColor(body, "sem observações tipadas nesta fonte", theme.Warn)
	case !st.Have:
		centered(body, "Coletando baseline; ainda sem conclusão.")
	case len(st.Rows) == 0 && st.Snapshot.Coverage.State == insight.CoverageComplete:
		centered(body, "Nenhum sinal ativo nos dados observados.")
	case len(st.Rows) == 0:
		centeredColor(body, "Sem conclusão: cobertura "+coverage+".", theme.Warn)
	default:
		off := ScrollOffset(st.Cursor, len(st.Rows), rows)
		for i := 0; i < rows && off+i < len(st.Rows); i++ {
			idx := off + i
			style := func(c theme.RGB) draw.Style { return draw.Fg(c) }
			if !st.Gone && idx == st.Cursor {
				style = func(c theme.RGB) draw.Style { return draw.Fg(c).WithBg(theme.BorderFaint).WithBold() }
			}
			line := radarRow(cols, inner.W, st.Rows[idx], style)
			if !st.Gone && idx == st.Cursor {
				line[0] = draw.S("> ", style(ctx.Pal.A))
			}
			body.Line(0, i, line.Truncate(inner.W))
		}
	}
	if inner.H > rows+RadarListTop {
		hint := fmt.Sprintf("%d sinais · High/Warn ativos, rec. obs. susp res.", len(st.Rows))
		inner.Line(0, inner.H-1, draw.Line{faint(hint)}.Truncate(inner.W))
	}
}

func evidenceLines(s insight.Signal) []draw.Line {
	e := s.Evidence
	var out []draw.Line
	kv := func(k, v string) { out = append(out, draw.Line{muted(k + ": "), text(v)}) }
	switch s.Rule {
	case insight.RuleCrashLoop, insight.RuleImagePull:
		kv("Motivo de espera", reasonLabel(e.Reason))
		kv("Observações distintas", fmt.Sprintf("%d (exige 2 versões do pod)", e.Observations))
		kv("Duração observada", durationText(e.Duration))
	case insight.RuleRestarts:
		kv("Contador", fmt.Sprintf("%d -> %d", e.CounterBefore, e.CounterAfter))
		kv("Restarts na janela de 5min", fmt.Sprintf("%d (Warning >= 3, High >= 10)", e.WindowRestarts))
		out = append(out, draw.Line{faint("Contador da primeira observação é linha de base, não taxa.")})
	case insight.RuleOOM:
		kv("Terminação", reasonLabel(e.Reason)+fmt.Sprintf("; exit code %d", e.ExitCode))
		kv("Terminou (fonte)", clockText(e.EventAt))
		kv("Recebido", clockText(e.ReceivedAt))
		out = append(out, draw.Line{faint("Limite/pico não observado; causa não confirmada.")})
	case insight.RulePending:
		kv("Fase do pod", podPhaseLabel(e.Phase))
		if e.NodeName == "" {
			kv("Node", "nenhum atribuído (aguardando agendamento)")
		} else {
			kv("Node", draw.Sanitize(e.NodeName)+" (atribuído; aguardando containers)")
		}
		kv("Duração observada", durationText(e.Duration))
		out = append(out, draw.Line{faint("Criação do pod não é usada como início do Pending.")})
	case insight.RuleEvicted:
		kv("Transição observada", clockText(e.TransitionObservedAt))
		kv("Node", reasonOr(draw.Sanitize(e.NodeName), "não informado"))
	}
	return out
}

func podPhaseLabel(p insight.PodPhase) string {
	switch p {
	case insight.PodPending:
		return "Pending"
	case insight.PodRunning:
		return "Running"
	case insight.PodSucceeded:
		return "Succeeded"
	case insight.PodFailed:
		return "Failed"
	}
	return "não informada"
}

const radarOpenPod = "[Abrir pod]"

func RadarPodButton(st RadarState, width int) (Span, int, bool) {
	if !st.PodOpen {
		return Span{}, 0, false
	}
	return Span{X: 0, W: draw.Width(radarOpenPod)}, len(RadarDetailLines(st, width)) - 1, true
}

func RadarDetailLines(st RadarState, width int) []draw.Line {
	s := st.Selected
	if s == nil {
		return []draw.Line{{faint("nenhum sinal selecionado")}}
	}
	var lines []draw.Line
	if st.Gone {
		lines = append(lines, draw.Line{colored("Alvo não está mais no inventário observado; exibindo a última leitura.", theme.Warn)}, nil)
	}
	sev, color := severityLabel(s.Severity)
	peak, _ := severityLabel(s.PeakSeverity)
	pre := "não"
	if s.Preexisting {
		pre = "sim (já presente na primeira observação desta sessão)"
	}
	lines = append(lines,
		draw.Line{muted("Sinal: "), bold(RuleLabel(s.Rule), theme.Text), faint(" | regra v" + draw.Sanitize(s.RuleVersion))},
		draw.Line{muted("Alvo: "), text("Pod " + draw.Sanitize(s.Entity.Namespace+"/"+s.Entity.Name))},
	)
	if s.Container != "" {
		lines = append(lines, draw.Line{muted("Container: "), text(draw.Sanitize(s.Container) + " (" + containerKindLabel(s.ContainerKind) + ")")})
	}
	lines = append(lines,
		draw.Line{muted("Estado: "), text(signalStateLabel(s.State)), faint(" | "), muted("severidade "), colored(sev, color), faint(" | "), muted("pico " + peak)},
		draw.Line{muted("Preexistente: "), text(pre)},
		draw.Line{muted("Primeira observação: "), text(clockText(s.FirstObservedAt)), faint(" | "), muted("última: "), text(clockText(s.LastObservedAt))},
	)
	if !s.ResolvedAt.IsZero() {
		lines = append(lines, draw.Line{muted("Resolvido às: "), text(clockText(s.ResolvedAt))})
	}
	if s.Suspension != insight.SuspensionNone {
		lines = append(lines, draw.Line{colored("Suspenso: "+suspensionText(s.Suspension)+"; evidência preservada, sem conclusão nova.", theme.Warn)})
	}
	if s.Coverage.State != insight.CoverageComplete {
		label, c := coverageLabel(s.Coverage)
		lines = append(lines, draw.Line{muted("Cobertura do sinal: "), colored(label, c), faint(" " + draw.Sanitize(s.Coverage.Reason))})
	}
	lines = append(lines, nil, draw.Line{bold("EVIDÊNCIAS", theme.Text)})
	lines = append(lines, evidenceLines(*s)...)
	lines = append(lines, nil, draw.Line{bold("EXPLICAÇÃO", theme.Text)},
		draw.Line{faint("Causa provável e próximo passo ainda não são publicados pelo motor.")},
		draw.Line{faint("Os campos acima são observações, não causa confirmada.")},
		nil,
	)
	if st.PodOpen {
		lines = append(lines, draw.Line{bold(radarOpenPod, theme.Text), faint(" enter")})
	} else if !st.Gone {
		lines = append(lines, draw.Line{colored("Pod não está na lista atual; nada é aberto por homônimo.", theme.Warn)})
	}
	var out []draw.Line
	for _, l := range lines {
		if l == nil || l.Width() <= width {
			out = append(out, l)
			continue
		}
		out = append(out, wrapLine(l, width)...)
	}
	return out
}

func wrapLine(l draw.Line, width int) []draw.Line {
	var plain strings.Builder
	for _, s := range l {
		plain.WriteString(s.Text)
	}
	style := draw.Fg(theme.Text)
	if len(l) > 0 {
		style = l[len(l)-1].Style
	}
	var out []draw.Line
	for _, part := range draw.Wrap(plain.String(), max(1, width)) {
		out = append(out, draw.Line{draw.S(part, style)})
	}
	return out
}

func containerKindLabel(k insight.ContainerKind) string {
	switch k {
	case insight.ContainerInit:
		return "init"
	case insight.ContainerSidecar:
		return "sidecar"
	case insight.ContainerEphemeral:
		return "efêmero"
	}
	return "regular"
}

func RadarDetailRows(panelH int) int {
	return max(0, panelH-2)
}

func RadarDetail(r draw.Region, st RadarState, ctx Ctx) {
	inner := Panel(r, "Evidências do sinal", false, ctx).Inset(1, 0, 1, 0)
	if inner.Empty() {
		return
	}
	lines := RadarDetailLines(st, inner.W)
	scroll := max(0, min(st.Scroll, len(lines)-inner.H))
	for y := 0; y < inner.H && scroll+y < len(lines); y++ {
		inner.Line(0, y, lines[scroll+y].Truncate(inner.W))
	}
}

func RadarCoverageLines(st RadarState) []draw.Line {
	if !st.Available || !st.Have {
		return []draw.Line{{faint("Cobertura: sem leitura do motor ainda.")}, {faint("Rede indisponível; nodes/proxy não é utilizado.")}}
	}
	c := st.Snapshot.Coverage
	label, color := coverageLabel(c)
	line := draw.Line{muted("Cobertura: inventário de pods "), colored(label, color)}
	if c.Total > 0 || c.State == insight.CoverageComplete {
		line = append(line, muted(fmt.Sprintf(" %d/%d", c.Observed, c.Total)))
	}
	if c.Reason != "" {
		line = append(line, faint("; "+draw.Sanitize(c.Reason)))
	}
	line = append(line, faint(" | eventos, owners, nodes e métricas: ainda não consumidos pelo Radar"))
	return []draw.Line{line, {faint("Rede indisponível; nodes/proxy não é utilizado. Histórico limitado a esta sessão.")}}
}

func RadarCoverage(r draw.Region, st RadarState, ctx Ctx) {
	inner := Panel(r, "Cobertura", false, ctx).Inset(1, 0, 1, 0)
	for y, l := range RadarCoverageLines(st) {
		if y >= inner.H {
			break
		}
		inner.Line(0, y, l.Truncate(inner.W))
	}
}

func RadarHelp() []KeyHelp {
	return []KeyHelp{
		{"↑↓", "move"},
		{"enter", "open pod"},
		{"esc", "back"},
		{"m", "menu"},
		{"?", "help"},
		{"q", "quit"},
	}
}

func FullHelpRadar() []KeyHelp {
	return []KeyHelp{
		{"↑/↓ k/j", "move selection"},
		{"pgup/pgdn", "scroll evidence"},
		{"home/end", "first / last signal"},
		{"enter", "open pod of the signal"},
		{"esc", "back"},
		{"m", "screens menu ← → enter"},
		{"2", "Dashboard screen"},
		{"3", "Workloads screen"},
		{"4", "Nodes screen"},
		{"5", "Security screen"},
		{"6", "Events screen"},
		{"7", "Radar screen"},
		{"c n", "switch context / namespace"},
		{"A", "ack all Security alerts"},
		{"p", "cycle palette"},
		{"ctrl+p", "pause animations"},
		{"?", "toggle help"},
		{"q ctrl+c", "quit"},
	}
}
