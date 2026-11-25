package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/netpath"
	"github.com/vynazevedo/lazykube/internal/theme"
)

const (
	originW       = 15
	accessCaveat  = "estado pelos endpoints lidos; nenhuma requisição foi feita"
	accessReading = "lendo Services, Ingresses e EndpointSlices…"
)

type AccessView struct {
	Loading  bool
	Gone     bool
	Err      string
	Selected int
	Report   *netpath.Report
}

func stateHealth(s netpath.State) Health {
	switch s {
	case netpath.StateReady:
		return HealthOK
	case netpath.StateNoBackend:
		return HealthFailed
	}
	return HealthWarn
}

func stepHealth(s netpath.Status) Health {
	switch s {
	case netpath.StatusOK:
		return HealthOK
	case netpath.StatusBroken:
		return HealthFailed
	}
	return HealthWarn
}

func detailRow(label, value string) DescribeRow {
	return DescribeRow{Kind: RowDetail, Label: label, Value: value}
}

func AccessRows(v AccessView) []DescribeRow {
	head := DescribeRow{Kind: RowSection, Label: "Acesso"}
	switch {
	case v.Err != "":
		head.Value = v.Err
		return []DescribeRow{head}
	case v.Gone:
		head.Value = "o objeto não está mais na lista lida"
		return []DescribeRow{head}
	case v.Report == nil:
		head.Value = accessReading
		return []DescribeRow{head}
	}
	r := v.Report
	head.Value = plural(len(r.Addresses), "endereço", "endereços") + " · " + accessCaveat
	if v.Loading {
		head.Value = "leitura em andamento · " + head.Value
	}
	rows := []DescribeRow{head}
	for i, a := range r.Addresses {
		rows = append(rows, addressRows(a, i == v.Selected)...)
	}
	if len(r.Addresses) == 0 && !v.Loading {
		rows = append(rows, DescribeRow{Kind: RowHint, Value: "nenhum endereço derivado dos objetos lidos"})
	}
	for _, n := range r.Notes {
		rows = append(rows, DescribeRow{Kind: RowHint, Value: n})
	}
	if r.Truncated {
		rows = append(rows, DescribeRow{Kind: RowHint, Value: "lista limitada; há mais endereços ou rotas"})
	}
	if len(r.Addresses) > 0 {
		rows = append(rows, DescribeRow{Kind: RowHint, Value: "u próxima URL · y copia a selecionada"})
	}
	if len(r.Routes) > 0 {
		rows = append(rows, DescribeRow{Kind: RowBlank}, DescribeRow{Kind: RowSection, Label: "Rota", Value: "primeiro elo quebrado pelos objetos lidos"})
		for _, route := range r.Routes {
			rows = append(rows, routeRows(route)...)
		}
	}
	return append(rows, policyRows(r.Policies)...)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func addressRows(a netpath.Address, selected bool) []DescribeRow {
	rows := []DescribeRow{{Kind: RowAddress, Health: stateHealth(a.State), Label: a.Origin.String(), Value: a.Display, Selected: selected, Link: a.URL}}
	parts := []string{a.State.String(), a.Source}
	if a.Scheme != "" {
		parts = append(parts, a.Scheme)
	}
	rows = append(rows, detailRow("estado", strings.Join(parts, " · ")), detailRow("alcance", a.Reach.String()))
	if a.Reason != "" {
		rows = append(rows, detailRow("motivo", a.Reason))
	}
	if a.Refused != "" {
		rows = append(rows, detailRow("sem link", a.Refused))
	}
	if a.URL.Punycode() {
		rows = append(rows, detailRow("atenção", "host em punycode (xn--): confira o domínio antes de abrir"))
	}
	return rows
}

func routeRows(route netpath.Route) []DescribeRow {
	health := HealthOK
	if step, ok := route.First(); ok {
		health = stepHealth(step.Status)
	}
	rows := []DescribeRow{{Kind: RowNote, Health: health, Label: "", Value: route.Title}, {Kind: RowHint, Value: route.Verdict()}}
	for _, s := range route.Steps {
		rows = append(rows, DescribeRow{Kind: RowNote, Health: stepHealth(s.Status), Label: string(s.Kind), Value: s.Evidence})
	}
	return rows
}

func policyRows(p netpath.PolicyReport) []DescribeRow {
	if p.Note == "" {
		return nil
	}
	rows := []DescribeRow{{Kind: RowBlank}, {Kind: RowSection, Label: "NetworkPolicies", Value: p.Note}}
	switch {
	case len(p.Selecting) > 0:
		rows = append(rows, detailRow("selecionam", strings.Join(p.Selecting, ", ")))
	case p.Known && len(p.Uncertain) == 0:
		rows = append(rows, detailRow("selecionam", "nenhuma política lida seleciona este alvo"))
	}
	if len(p.Uncertain) > 0 {
		rows = append(rows, detailRow("incertas", strings.Join(p.Uncertain, ", ")))
	}
	return rows
}

func addressRow(r draw.Region, y int, row DescribeRow, ctx Ctx) {
	glyph, gc := healthGlyph(row.Health, ctx)
	r.Text(0, y, glyph, draw.Fg(gc))
	r.Text(2, y, draw.PadRight(row.Label, originW-2), draw.Fg(theme.Muted))
	style := draw.Fg(theme.Text)
	if row.Selected {
		style = draw.Fg(ctx.Pal.A).WithBold()
	}
	limit := r.W
	if row.Selected {
		limit = r.LineRight(y, draw.Line{colored("◂ y", ctx.Pal.A)}) - 1
	}
	width := max(0, limit-originW)
	if draw.Width(row.Value) <= width && row.Value == row.Link.String() {
		style = style.WithLink(row.Link)
	}
	r.Text(originW, y, draw.Truncate(row.Value, width), style)
}

func ScrollToSelection(rows []DescribeRow, scroll, visible int) int {
	for i, row := range rows {
		if row.Kind != RowAddress || !row.Selected {
			continue
		}
		last := i + 1
		switch {
		case i < scroll:
			return i
		case visible > 0 && last >= scroll+visible:
			return last - visible + 1
		}
		return scroll
	}
	return scroll
}

type ResourceDetailState struct {
	Kind       string
	Net        data.NetworkKind
	Workload   bool
	Row        *data.ResourceRow
	Coverage   data.Coverage
	ObservedAt time.Time
	Synthetic  bool
	Scroll     int
	Access     *AccessView
}

func ResourceDetailRows(panelH int) int {
	return max(0, panelH-2)
}

func (st ResourceDetailState) Rows() []DescribeRow {
	if st.Row == nil {
		return nil
	}
	rows := st.fieldRows()
	if st.Access != nil {
		rows = append(append(rows, DescribeRow{Kind: RowBlank}), AccessRows(*st.Access)...)
	}
	return rows
}

func (st ResourceDetailState) fieldRows() []DescribeRow {
	var rows []DescribeRow
	add := func(label, value string) {
		if value == "" {
			value = "-"
		}
		rows = append(rows, DescribeRow{Kind: RowField, Label: label, Value: value})
	}
	ref := st.Row.Ref
	add("Nome", ref.Name)
	if ref.Namespace != "" {
		add("Namespace", ref.Namespace)
	}
	if !ref.CreatedAt.IsZero() && !st.ObservedAt.IsZero() {
		add("Idade", since(ref.CreatedAt, st.ObservedAt))
	}
	if st.Workload {
		w := st.Row.Workload
		if w.Kind != "" {
			add("Estado", string(w.State))
			add("Ready/Desired", ratio(w.Ready, w.Desired))
		}
		if w.Reason != "" {
			add("Motivo", w.Reason)
		}
		return rows
	}
	if cov, _ := resourceCoverageText(st.Coverage); st.Coverage.State != "" {
		add("Cobertura", cov)
	}
	f := st.Row.Network
	if f == nil {
		return rows
	}
	if f.Truncated {
		add("Atenção", "objeto grande ou ilegível: só parte dos dados foi lida")
	}
	switch {
	case f.Service != nil:
		serviceFields(add, *f.Service)
	case f.Slice != nil:
		sliceFields(add, *f.Slice)
	case f.Ingress != nil:
		ingressFields(add, *f.Ingress)
	case f.Class != nil:
		add("Controller", f.Class.Controller)
		add("Padrão", triText(data.TriOf(f.Class.Default)))
	case f.Policy != nil:
		add("Pod selector", policySelector(*f.Policy))
		add("Tipos", strings.Join(f.Policy.Types, ", "))
		add("Regras", fmt.Sprintf("%d de entrada, %d de saída", f.Policy.IngressRules, f.Policy.EgressRules))
		add("Efeito", "depende do CNI e não foi avaliado")
	case f.Route != nil:
		add("Hostnames", strings.Join(f.Route.Hostnames, ", "))
		add("Parents", strings.Join(f.Route.Parents, ", "))
		add("Backends", strings.Join(f.Route.Backends, ", "))
		add("Regras", fmt.Sprint(f.Route.Rules))
		add("Accepted", triText(f.Route.Accepted))
		add("URL", "não derivada: esquema e porta dependem do Gateway, que não é lido")
	}
	return rows
}

func serviceFields(add func(string, string), s data.ServiceFacts) {
	add("Tipo", s.Type)
	add("ClusterIP", strings.Join(s.ClusterIPs, ", "))
	if s.ExternalName != "" {
		add("ExternalName", s.ExternalName)
	}
	add("Externo", serviceExternal(s))
	selector := s.Selector.String()
	if selector == "" {
		selector = "sem selector"
	}
	add("Selector", selector)
	for _, p := range s.Ports {
		text := servicePort(p)
		if p.Name != "" {
			text = p.Name + " " + text
		}
		if p.Target != "" {
			text += " para " + p.Target
		}
		add("Porta", text)
	}
}

func sliceFields(add func(string, string), s data.SliceFacts) {
	add("Service", s.Service)
	add("Família", s.AddressType)
	add("Portas", slicePorts(s.Ports))
	add("Prontos", fmt.Sprintf("%d de %d", s.Ready, s.Total))
	for _, e := range s.Endpoints {
		text := strings.Join(e.Addresses, ",") + " pronto " + triText(e.Ready)
		if e.Terminating == data.TriTrue {
			text += " encerrando"
		}
		if e.TargetName != "" {
			text += " " + e.TargetKind + "/" + e.TargetName
		}
		if e.Node != "" {
			text += " em " + e.Node
		}
		add("Endpoint", text)
	}
	if hidden := s.Total - len(s.Endpoints); hidden > 0 {
		add("Endpoint", fmt.Sprintf("mais %d não mostrados", hidden))
	}
}

func ingressFields(add func(string, string), i data.IngressFacts) {
	add("Classe", i.Class)
	if i.ClassAnnotation != "" {
		add("Classe legada", i.ClassAnnotation)
	}
	add("Endereço", balancers(i.Addresses))
	for _, t := range i.TLS {
		secret := t.Secret
		if secret == "" {
			secret = "sem secretName"
		}
		add("TLS", strings.Join(t.Hosts, ", ")+" "+secret+" (conteúdo não lido)")
	}
	for _, r := range i.Rules {
		for _, p := range r.Paths {
			host := r.Host
			if host == "" {
				host = "(qualquer host)"
			}
			add("Regra", host+p.Path+" "+p.Type+" para "+p.Backend.String())
		}
	}
	if i.Default != nil {
		add("Padrão", i.Default.String())
	}
}

func ResourceDetail(r draw.Region, st ResourceDetailState, ctx Ctx) {
	title := draw.Sanitize(st.Kind)
	if st.Row != nil {
		name := draw.Sanitize(st.Row.Ref.Name)
		if st.Row.Ref.Namespace != "" {
			name = qualifiedName(*st.Row)
		}
		title += " · " + name
	}
	if st.Synthetic {
		title += " · [DEMO]"
	}
	inner := Panel(r, title, true, ctx).Inset(1, 0, 1, 0)
	if inner.Empty() {
		return
	}
	rows := st.Rows()
	if len(rows) == 0 {
		centered(inner, "nenhum objeto selecionado")
		return
	}
	for i := 0; i < inner.H && st.Scroll+i < len(rows); i++ {
		describeRow(inner, i, rows[st.Scroll+i], ctx)
	}
}

func ResourceDetailHelp() []KeyHelp {
	return []KeyHelp{{"↑↓", "scroll"}, {"u", "next URL"}, {"y", "copy"}, {"r", "reload"}, {"esc", "back"}, {"b", "tree"}, {"m", "menu"}, {"?", "help"}, {"q", "quit"}}
}

func FullHelpResourceDetail() []KeyHelp {
	return []KeyHelp{
		{"↑/↓ k/j", "scroll"},
		{"pgup/pgdn", "page"},
		{"home/end", "top / bottom"},
		{"u", "next URL in Access"},
		{"y", "copy selected URL or name"},
		{"r", "reload lists"},
		{"esc", "back"},
		{"b", "navigation tree"},
		{"m", "screens menu ← → enter"},
		{"2 3 4 6", "Dashboard / Workloads / Nodes / Events"},
		{"c n", "switch context / namespace"},
		{"A", "ack all Security alerts"},
		{"p", "cycle palette"},
		{"ctrl+p", "pause animations"},
		{"?", "toggle help"},
		{"q ctrl+c", "quit"},
	}
}
