lazykube 2 - Especificação da TUI (Bubble Tea)

> **Para o Claude Code:** este documento é a especificação completa de uma TUI para Kubernetes, escrita em Go com o ecossistema Charm (Bubble Tea v2). Ela foi derivada de um mockup de alta fidelidade. Implemente **fielmente**: cores, glyphs, layout em células, animações e comportamento estão definidos aqui com valores exatos. Quando algo for ambíguo, prefira o que este documento diz a qualquer padrão de biblioteca. No final há critérios de aceite. Use-os como checklist antes de declarar o trabalho pronto.

---

## 0. Resumo

Uma TUI full-screen para inspecionar um cluster Kubernetes, com:

- 3 colunas: **Node + Namespaces + Deployments** | **Pods (foco) + Heatmap de CPU** | **Painel de detalhes com abas** (Log, Config, Top, Describe, Events).
- **Imagens 2D** renderizadas com half-blocks (`▀`): um sprite de rack de servidor com LEDs piscando, e um heatmap de CPU por pod rolando no tempo.
- **Efeitos visuais**: logo com gradiente animado, borda com gradiente animado no painel focado, spinner braille, pontos de status pulsando, cursor piscando, gráfico de CPU em braille com área em gradiente, barras de progresso com gradiente, sparklines, transições suaves (harmonica).
- **Modo demo** (`--demo`) com dados sintéticos determinísticos, idênticos ao mockup, e **modo real** (client-go + metrics.k8s.io).

Terminal de referência: **160×48**, truecolor. Fonte do mockup: JetBrains Mono, mas isso é responsabilidade do terminal do usuário.

---

## 1. Stack e versões

| Papel | Pacote | Observação |
| --- | --- | --- |
| Runtime / loop | `charm.land/bubbletea/v2` | **v2** (última estável na série v2.0.x). `View()` retorna `tea.View`. Alt screen e mouse são campos do `View`. |
| Estilo / layout | `charm.land/lipgloss/v2` | `JoinHorizontal/JoinVertical`, `Place`, bordas, `lipgloss.Color("#hex")`. |
| Componentes | `charm.land/bubbles/v2` | `table` (opcional, ver §6.4), `textinput`, `viewport`, `progress`, `spinner`, `help`, `key`. |
| Animação física | `github.com/charmbracelet/harmonica` | Springs para transições. |
| Cores / blend | `github.com/lucasb-eyer/go-colorful` | `BlendLuv` / `BlendRgb` para gradientes. |
| Mouse zones | `github.com/lrstanley/bubblezone` | **Verifique compatibilidade com Bubble Tea v2.** Se não houver, implemente hit-testing próprio (as coordenadas do layout são determinísticas, ver §4). |
| Gráficos | **Implementação própria** (braille canvas) | `NimbleMarkets/ntcharts` é opcional. Se a versão disponível não for compatível com lipgloss v2, faça o canvas braille em casa (≈80 linhas, algoritmo em §8.3). O resultado precisa bater com o visual especificado de qualquer forma. |
| Imagem real (opcional) | `github.com/blacktop/go-termimg` | Só para exibir imagens raster reais (Kitty/Sixel/iTerm2). O sprite e o heatmap **não** usam isso: são half-blocks próprios (§7). |
| Kubernetes | `k8s.io/client-go`, `k8s.io/metrics` | Informers e PodMetrics. |
| Fuzzy filter | `github.com/sahilm/fuzzy` | Filtro de pods. |
| Highlight YAML | `github.com/alecthomas/chroma/v2` | Aba Config (opcional; fallback: colorir chave/valor manualmente, §6.8). |

Antes de codar, rode `go get` de cada dependência e confirme as APIs reais da versão resolvida (v2 mudou bastante: `tea.KeyPressMsg`, `key.Code`/`key.Text`, `tea.MouseClickMsg`, `tea.NewView`, `v.AltScreen = true`, `v.MouseMode = tea.MouseModeCellMotion`). Se alguma API citada aqui não existir na versão resolvida, adapte mantendo o resultado visual.

Go ≥ 1.23.

---

## 2. Estrutura do projeto

```
lazykube/
├── cmd/lazykube/main.go          # flags: --demo, --context, --kubeconfig, --palette, --no-anim, --fps
├── internal/
│   ├── app/
│   │   ├── model.go              # Model raiz, Init/Update/View, foco, layout
│   │   ├── keys.go               # KeyMap (bubbles/key) + help
│   │   ├── msgs.go               # frameMsg, podsMsg, metricsMsg, logLineMsg, eventsMsg...
│   │   └── layout.go             # cálculo de retângulos (§4) + hit-testing
│   ├── theme/
│   │   ├── palette.go            # tokens de cor e paletas (§3)
│   │   └── gradient.go           # blend, gradientText, gradientBorder (§8.1, §8.2)
│   ├── ui/
│   │   ├── header.go
│   │   ├── node.go               # painel Node + sprite (§7.1)
│   │   ├── namespaces.go
│   │   ├── deployments.go
│   │   ├── pods.go               # tabela + filtro
│   │   ├── heatmap.go            # §7.2
│   │   ├── details.go            # abas e conteúdo
│   │   ├── top.go                # gráfico CPU, memória, rede, mini-logs
│   │   ├── footer.go
│   │   └── panel.go              # caixa com borda arredondada + título embutido (§5)
│   ├── draw/
│   │   ├── halfblock.go          # matriz de pixels → string ▀
│   │   ├── braille.go            # canvas braille 2×4
│   │   └── spark.go              # sparkline e mini-barras
│   ├── data/
│   │   ├── source.go             # interface Source
│   │   ├── demo.go               # dados sintéticos determinísticos (§9)
│   │   └── kube.go               # client-go + metrics (§10)
│   └── anim/
│       ├── clock.go              # relógio de animação (§8)
│       └── spring.go             # wrappers harmonica
└── go.mod
```

---

## 3. Design tokens

### 3.1 Cores fixas (todas truecolor)

| Token | Hex | Uso |
| --- | --- | --- |
| `bgApp` | `#0B0C12` | fundo geral da tela |
| `bgPanel` | `#10111A` | fundo dentro dos painéis |
| `bgInset` | `#0D0E15` | caixas internas (logs, YAML, describe, events) |
| `bgCard` | `#141522` | cards de Memória/Network |
| `bgChip` | `#161827` | input de filtro, pill de contexto |
| `bgChip2` | `#121420` | pill de status (direita do header) |
| `border` | `#2A2C3D` | bordas de painéis não focados |
| `borderSoft` | `#1E2030` | divisores internos, trilho de barras |
| `borderFaint` | `#1A1C2A` | divisor do rodapé, fundo de keycap |
| `text` | `#E6E6F0` | texto principal |
| `textSoft` | `#C9CBDD` | texto de logs, mensagens |
| `textMid` | `#B9BBD0` | rótulo "metrics" |
| `muted` | `#8A8CA3` | rótulos, colunas secundárias |
| `faint` | `#5C5F7A` | timestamps, números de linha |
| `dimGlyph` | `#6B6E88` | glyph de pod Completed, nível DEBUG |
| `ok` | `#5FE3A1` | Running, Ready, Normal, rx |
| `warn` | `#FFC857` | Pending, Warning, restarts > 0, tx |
| `err` | `#FF6B6B` | ERROR |
| `white` | `#FFFFFF` | nome do pod selecionado |

### 3.2 Paletas de destaque (flag `--palette`, padrão `charm`)

| Paleta | `a` (primária) | `b` (secundária) | `c` (terciária) |
| --- | --- | --- | --- |
| `charm` | `#FF5FAF` | `#9D7CFF` | `#5FD7FF` |
| `ocean` | `#4FC3F7` | `#5C7CFA` | `#63E6BE` |
| `matrix` | `#B6FF5C` | `#3DDC84` | `#9CF5D0` |

Tecla `p` cicla as paletas em runtime.

Onde cada uma aparece:

- **a**: título do painel focado, cursor do filtro, `/` do filtro, spinner do header, linha tracejada "request", memória (valor e fim do gradiente), LED de energia do sprite, prompt `❯` da aba Log, alta intensidade no heatmap.
- **b**: barras de namespace, início dos gradientes, meio do heatmap, chaves YAML, moldura do sprite (escurecida).
- **c**: títulos de painéis não focados, contexto do header, valor de CPU, aba ativa (fundo), INFO nos logs, mini-barras de CPU na tabela, LEDs de atividade do sprite, ponto final do gráfico.

### 3.3 Funções de cor (implementar em `theme`)

```go
// mix: interpolação linear RGB entre h1 e h2 com k ∈ [0,1]
func Mix(h1, h2 string, k float64) color.Color
// noise determinístico (usado em TODO o modo demo e nos efeitos)
func Noise(i float64) float64 { x := math.Sin(i*12.9898+78.233) * 43758.5453; return x - math.Floor(x) }
```

`Mix` deve ser RGB linear simples (é o que o mockup usa). Gradientes de texto/borda podem usar `BlendLuv` se ficarem mais bonitos, mas mantenha os pontos de parada.

### 3.4 Glyphs

| Uso | Glyph |
| --- | --- |
| Running | `●` (cor `ok`) |
| Completed | `○` (cor `dimGlyph`) |
| Pending / ContainerCreating | frame do spinner braille (cor `warn`) |
| Spinner | `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏` (10 frames) |
| Sparkline | `▁▂▃▄▅▆▇█` |
| Mini-barra cheia / vazia | `█` / `░` |
| Pixel 2D | `▀` (fg = pixel de cima, bg = pixel de baixo) |
| Linha tracejada | `╌` |
| Bordas | arredondadas: `╭ ╮ ╰ ╯ ─ │` |
| Rede | `↓` rx, `↑` tx |
| Prompt | `❯` |
| Namespace vazio | `·····` |

Nunca use emoji.

---

## 4. Layout em células

### 4.1 Grade de referência (160×48)

```
col 0                                                                                                                                  159
┌─ header (linha 0) ──────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│ lazykube v2.0  ( ◎ context huawei_k3s )  k8s v1.28.4+k3s2  nodes 1  ns 8  pods 13  uptime 119d            [ ⠋ metrics every 5s ● live ] │
└─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
 linha 1: vazia
 linhas 2..45: área principal (44 linhas)
╭─ Node ─────────────────────────╮ ╭─ Pods · focus ─────────────────────────────────────╮ ╭──────────────────────────────────────────────╮
│ [sprite 20×12]  huawei-k3s-01  │ │ / filter pods… ▌                         13 items  │ │ Log  Config [Top] Describe  Events     tab ⇥ │
│                 ● Ready        │ │ NAME              NAMESPACE      RST  AGE  CPU     │ │──────────────────────────────────────────────│
│                 cpu 22%        │ │ ● bilibili-raffle… bilibili-raf…   0  119d ██░░░░░ │ │ ● bilibili-live-7f6b8d9c5-mn4tz  [Running]   │
│                 ███████░░░░    │ │ ● bilibili-live-…  (selecionado)  2  119d ███░░░░ │ │   bilibili-raffle · Running · node … · 10.42… │
│                 mem 1.4Gi/3.8Gi│ │ …                                                   │ │ CPU 10m  req 10m · lim 50m                    │
│                 ████░░░░░░░    │ │                                                     │ │ 20m ┤⣀⣠⣤⣴⣶ (braille, área em gradiente)       │
│                 pods 13 / 110  │ │                                                     │ │ …                                            │
│                 arch amd64     │ ╰─────────────────────────────────────────────────────╯ │  0  ┴────────────────────────────────────── │
╰────────────────────────────────╯ ╭─ CPU heatmap · last 2 min ─────────────────────────╮ │ ╭ Memory ───────────╮ ╭ Network ─────────────╮│
╭─ Namespaces ───────────────────╮ │ bilibili-raffle ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀ │ │ │ 75Mi / 128Mi       │ │ ↓ 4.2 kB/s ↑ 1.3 kB/s ││
│ default          ·····       0 │ │ …                                                   │ │ ╰───────────────────╯ ╰──────────────────────╯│
│ …                              │ │ -2m ▀▀▀▀▀▀▀ gradiente ▀▀▀▀▀▀▀ now                   │ │ ╭ logs · tail -f ───────────────── viewport ╮│
╰────────────────────────────────╯ ╰─────────────────────────────────────────────────────╯ │ │ 20:14:03 INFO  ws connected room=…        ││
╭─ Deployments ──────────────────╮                                                         │ ╰───────────────────────────────────────────╯│
│ bilibili-raffle/bilibili-live ● 1/1                                                     ╰──────────────────────────────────────────────╯
╰────────────────────────────────╯
 linha 46: divisor (─ em borderFaint)
 linha 47: rodapé (keycaps de ajuda + "bubbletea · 60 fps")
```

(O diagrama é ilustrativo. As medidas exatas estão abaixo.)

### 4.2 Medidas

Seja `W×H` o tamanho do terminal.

- **Header**: linha 0. Linha 1 vazia.
- **Rodapé**: linha `H-2` = divisor `─` (cor `borderFaint`) em toda a largura; linha `H-1` = help.
- **Área principal**: linhas `2 .. H-3` ⇒ altura `M = H - 4` (44 em 48 linhas).
- **Margem lateral**: 1 coluna de cada lado. Largura útil `U = W - 2`.
- **Colunas** (gap de 1 coluna entre elas):
  - Esquerda `L = round(U × 0.234)` (36 em 158), mínimo 32.
  - Centro `C = round(U × 0.341)` (54), mínimo 46.
  - Direita `R = U - L - C - 2` (66 em 158), mínimo 50.
- **Coluna esquerda** (empilhada, gap 0, já que as bordas separam):
  - Node: altura **16** (borda 2 + sprite 12 linhas + padding vertical 1+1).
  - Namespaces: altura **12** (borda 2 + 8 linhas + padding 1+1).
  - Deployments: o restante (`M - 28`).
- **Coluna central**:
  - Pods: altura **19** (borda 2 + padding top 1 + filtro 1 + espaço 1 + cabeçalho 1 + 13 linhas). Se houver mais pods do que cabe, rola.
  - Heatmap: o restante (`M - 19`).
- **Coluna direita**: um único painel com altura `M`.

**Responsividade:**

- Abaixo de **120×36**, mostre apenas uma mensagem centralizada (`lipgloss.Place`), em `muted`: `terminal muito pequeno - mínimo 120×36 (atual W×H)`.
- Entre 120 e 140 colunas: esconda a coluna **Node/Namespaces/Deployments** e redistribua (Centro 45%, Direita 55%). A tecla `1` mostra ou esconde a coluna esquerda manualmente em qualquer largura.
- Altura extra vai para Deployments, para o Heatmap e para a área de logs da aba Top.

### 4.3 Componente `Panel` (borda arredondada com título embutido)

Todos os painéis usam o mesmo desenho:

```
╭─ Título ─────────────╮
│ conteúdo             │
╰──────────────────────╯
```

- Borda arredondada; cor `border` (não focado).
- Título embutido na linha superior a partir da coluna 2: `─ ` + título em **bold** + ` ─…`. Cor do título: `pal.c` (não focado) ou `pal.a` (focado).
- Padding interno: 1 coluna horizontal. Padding vertical conforme cada painel.
- Fundo interno: `bgPanel`. **Todas as células internas precisam ter o background pintado**, inclusive espaços. Use `lipgloss.Style.Background` + `Width/Height` ou preencha manualmente, para não vazar `bgApp`.
- **Painel focado**: a borda é desenhada com **gradiente animado** (§8.2) em vez de `border`.

Implemente a borda manualmente (rune a rune) para suportar o gradiente por célula. Não dependa de `BorderForeground` de cor única para o painel focado.

---

## 5. Header (linha 0)

Da esquerda para a direita, separados por 2 espaços:

1. **Logo** `lazykube` em **bold**, com gradiente por rune `a → b → c → a`, deslocado pela fase do relógio (§8.1). Ciclo de 6 s.
2. `v2.0` em `muted`.
3. **Pill de contexto**: fundo `bgChip`, padding 1 col: `◎ ` (em `pal.c`) + `context ` (em `muted`) + `huawei_k3s` (bold, `pal.c`). Use o nome real do contexto atual.
4. Estatísticas em `muted` com valores em `text`: `k8s v1.28.4+k3s2`, `nodes 1`, `ns 8`, `pods 13`, `uptime 119d`.
5. Espaçador flexível.
6. **Pill de status** (alinhada à direita, fundo `bgChip2`): `⠋` (spinner em `pal.a`) + ` metrics` (`textMid`) + ` every 5s` (`muted`) + ` ●` (pulsando, `ok`) + ` live` (`ok`).
  - Se a última coleta de métricas falhou: `●` em `err` e texto `stale`.
  - Em `--no-anim`: o spinner fica parado no primeiro frame.

---

## 6. Painéis

### 6.1 Node (coluna esquerda, topo)

- Título: `Node`.
- Esquerda: **sprite 2D do rack** (20 px × 24 px ⇒ 20 colunas × 12 linhas em half-block), alinhado ao fundo. Abaixo/atrás do sprite fica um **"glow"**: a linha imediatamente abaixo do sprite (ou a última linha do sprite, nas células transparentes) recebe fundo `mix(bgPanel, pal.c, g)`, onde `g` oscila entre 0.10 e 0.22 em ciclo de 2.4 s (senoidal). É um brilho sutil, não uma faixa sólida.
- Direita (2 colunas após o sprite), uma informação por linha:
  1. `huawei-k3s-01` (bold, `text`)
  2. `● Ready` (`●` pulsando, tudo em `ok`)
  3. `cpu ` (`muted`) + `22%` (`text`)
  4. barra de CPU do node: largura até o fim do painel, trilho `borderSoft`, preenchimento com gradiente horizontal `c → b` (use `bubbles/progress` com `WithColors`/gradiente equivalente na v2, ou desenhe com `█` por célula)
  5. `mem ` + `1.4Gi / 3.8Gi`
  6. barra de memória do node: gradiente `b → a`, 37%
  7. `pods ` + `13 / 110`
  8. `arch ` + `amd64`

### 6.2 Namespaces

- Título: `Namespaces`.
- Uma linha por namespace: `nome` (preenche) | barra de 5 células | contagem de pods (alinhada à direita, `muted`).
- Barra: `█`×round(pods/5×5) + `░` no restante, em `pal.b`. Se a contagem for 0: `·····` em `pal.b`.
- O nome do namespace do pod selecionado fica em `pal.a`; os demais em `text`.
- Ordem (demo): `default 0`, `kube-system 5`, `kube-public 0`, `kube-node-lease 0`, `bilibili-raffle 2`, `lgo 1`, `xcx 3`, `ys-odoo 2`.
- Focável (Tab). Com foco, `↑↓` move a seleção, `enter` filtra os pods por namespace e `esc` limpa o filtro.

### 6.3 Deployments

- Título: `Deployments`.
- Linha: `ns/` (em `muted`) + `nome` (em `text`) | `●` (verde se ready == desired, `warn` se parcial, `err` se 0) | `1/1` (`muted`, alinhado à direita).
- Truncar com `…` quando não couber.

### 6.4 Pods (painel focado por padrão)

- Título: `Pods · focus` quando focado, `Pods` quando não.
- Borda com gradiente animado quando focado.
- **Linha de filtro** (fundo `bgChip`, largura total interna):
  `/` (`pal.a`) + ` filter pods…` (`muted`, placeholder) + cursor de bloco (`pal.a`, pisca a 1 s) + espaçador + `13 items` (`muted`).
  Use `bubbles/textinput`. `/` foca o filtro, `esc` sai e `enter` confirma. O filtro é fuzzy sobre `nome` e `namespace`.
- **Cabeçalho** (`muted`, maiúsculas): colunas `[2] NAME [flex] NAMESPACE [15] RST [4] AGE [5] CPU [7]`, separadas por 1 espaço. RST e AGE alinhados à direita.
- **Linhas**:
  - Coluna 1: glyph de status (§3.4) na cor do status.
  - NAME: `text` se Running, `muted` caso contrário. Truncar com `…`.
  - NAMESPACE: `muted`, truncar com `…`.
  - RST: `warn` se > 0, senão `muted`.
  - AGE: `muted`.
  - CPU: mini-barra de 7 células: `█`×round(cpu/20m×7) + `░` restante, em `pal.c`.
- **Linha selecionada**: fundo `mix(bgPanel, pal.b, 0.22)` em toda a largura interna, nome em `white` **bold**. A transição de fundo ao trocar a seleção é um **fade de ~180 ms** (harmonica spring ou interpolação linear do `k` de 0 → 0.22 na nova linha e 0.22 → 0 na antiga).
- Hover do mouse (MouseMotion): fundo `#1A1C2A` na linha sob o ponteiro.
- Clique numa linha seleciona o pod. Roda do mouse rola a lista.
- Recomendação: renderize a tabela à mão (lipgloss) em vez de `bubbles/table`, porque cada célula precisa de cor própria e o fundo da linha selecionada precisa cobrir as cores. Se usar `bubbles/table`, garanta o mesmo resultado.

### 6.5 Heatmap de CPU (imagem 2D de dados)

- Título: `CPU heatmap · last 2 min`.
- Rótulos à esquerda (14 colunas, `muted`): os 2 primeiros segmentos do nome do pod separados por `-` (ex.: `bilibili-raffle`, `bilibili-live`, `lgo-7c5d97584`, `psql-6bc7744f9`, `odoo-7575bf6d8`, `coredns-6799fbcd5`, `metrics-server`, `traefik-f4564c4f4`).
- Grade: 8 pods (apenas Running), `N` amostras de tempo. `N` = largura disponível após os rótulos (≈36 em 160 colunas; o mockup mostra 30).
- **Renderização**: cada pod ocupa **2 linhas de terminal**, com o mesmo esquema de half-block: a linha 1 é `█` na cor do valor e a linha 2 é `▀` com fg = cor do valor e bg = `bgPanel`. Isso dá uma "célula" de 1,5 linha de altura com 0,5 linha de respiro, imitando o gap do mockup. Cada amostra ocupa 1 coluna, sem gap horizontal.
- **Escala de cor** (`k ∈ [0,1]`):
  - `k < 0.5` → `mix(#161827, pal.b, k×2)`
  - `k ≥ 0.5` → `mix(pal.b, pal.a, (k−0.5)×2)`
- **Legenda** (última linha útil): alinhada sob a grade: `-2m ` + barra de gradiente `#161827 → b → a` (com `▀`/`█`) + ` now`, tudo em `muted` exceto a barra.
- A grade rola para a esquerda a cada nova amostra (a coluna mais recente fica à direita).

### 6.6 Painel de detalhes (coluna direita)

Estrutura de cima para baixo:

1. **Barra de abas**: `Log Config Top Describe Events`, cada uma com padding de 1 col.
  - Ativa: fundo `pal.c`, texto `bgApp`, **bold**.
  - Inativa: texto `muted` (hover: `text`).
  - À direita, em `muted`: `tab ⇥ / shift+tab`.
  - Linha seguinte: divisor `─` em `borderSoft`.
  - Aba padrão: **Top**. Teclas: `l` Log, `c` Config, `t` Top, `d` Describe, `e` Events, `[`/`]` aba anterior/próxima. Clique do mouse também troca.
  - Troca de aba: o conteúdo novo faz um "slide-in" horizontal de 3-4 colunas → 0 com spring harmonica (~200 ms). Opcional, desativado em `--no-anim`.
2. **Cabeçalho do pod selecionado** (2 linhas):
  - Linha 1: glyph de status + ` ` + nome (bold) + espaçador + badge `[Running]` com borda/texto na cor do status. Na v2, use um chip de 1 linha: ` Running ` com texto na cor do status e colchetes `[`/`]` na mesma cor, para caber em 1 linha.
  - Linha 2 (`muted`): `bilibili-raffle · Running · node huawei-k3s-01 · 10.42.0.24`.
3. **Conteúdo da aba** (o resto).

### 6.7 Aba Top

De cima para baixo:

**a) Gráfico de CPU**

- Linha de título: `CPU` (`muted`) + ` 10m` (bold, `pal.c`) + ` req 10m · lim 50m` (`muted`) + espaçador + `streamline · braille 2×4` (`muted`).
- Área do gráfico: **9 linhas** de altura.
  - Eixo Y à esquerda (5 colunas, alinhado à direita, `muted`): rótulos `20m`, `15m`, `10m`, `5m`, `0` distribuídos nas linhas 0, 2, 4, 6, 8.
  - Eixo: `│` vertical e `─` horizontal em `border` (com `└` no canto).
  - Canvas braille: largura = resto, 9 linhas ⇒ `(w×2) × (9×4)` pontos.
  - Escala Y fixa: 0-20m.
  - **Linhas de grade** horizontais em 15m e 5m: `─` em `borderSoft`, desenhadas *atrás*.
  - **Linha de request** em 10m: `╌` em `pal.a`, atrás da série. Rótulo `request 10m` em `pal.a` sobre fundo `bgPanel`, colado à direita, 1 linha acima da linha tracejada.
  - **Área**: todas as células braille abaixo da linha recebem pontos preenchidos. A cor de cada linha de terminal é `mix(bgPanel, pal.c, 0.45 × (1 − profundidade))`, com profundidade 0 na linha da série e 1 no eixo. É o gradiente vertical do mockup (`c` a 45% de opacidade → transparente).
  - **Linha da série**: pontos braille da curva (interpolação entre amostras) com cor horizontal `pal.b → pal.c` (esquerda → direita), sobrepostos à área.
  - **Ponto final**: `●` em `pal.c` na posição da última amostra.
  - 60 amostras na largura toda; o gráfico rola para a esquerda.

**b) Linha com dois cards lado a lado** (larguras iguais, 1 col de gap, fundo `bgCard`, borda arredondada `borderSoft`, 5 linhas cada):

- **Memory**: linha 1 `Memory` (`muted`) + ` 75Mi` (bold, `pal.a`) + ` / 128Mi` (`muted`); linha 2 barra de progresso com gradiente `b → a` sobre trilho `borderSoft` (anima a largura com spring, como `bubbles/progress`); linha 3 sparkline de memória em `pal.a`.
- **Network**: linha 1 `Network` (`muted`) + ` ↓ 4.2 kB/s` (`ok`) + ` ↑ 1.3 kB/s` (`warn`); linha 2 sparkline rx em `ok`; linha 3 sparkline tx em `warn`.

**c) Mini-logs** (resto da altura, fundo `bgInset`, borda `borderSoft`):

- Cabeçalho: `logs · tail -f` (esquerda) e `viewport` (direita), ambos `muted`.
- Últimas linhas que couberem: `HH:MM:SS` (`faint`) + 2 espaços + nível em 5 colunas (cor por nível) + mensagem (`textSoft`, truncada com `…`).
- Cores dos níveis: `INFO` → `pal.c`, `DEBUG` → `dimGlyph`, `WARN` → `warn`, `ERROR` → `err`.

### 6.8 Aba Log

- Caixa `bgInset` ocupando todo o conteúdo, `bubbles/viewport` com follow (`GotoBottom` a cada linha nova, a menos que o usuário tenha rolado para cima; nesse caso, mostra `↓ N new` em `pal.a` no canto inferior direito).
- Mesmo formato de linha da §6.7c.
- Última linha: `❯` em `pal.a` + cursor de bloco piscando (`pal.a`).
- Teclas: `f` alterna follow, `w` alterna wrap, `g`/`G` topo/fim.

### 6.9 Aba Config

- Caixa `bgInset`, `viewport`. YAML do pod (`kubectl get pod -o yaml`, sem `managedFields`).
- Número da linha alinhado à direita em 2 colunas (`faint`) + 2 espaços + chave (`pal.b`) + valor (`textSoft`).
- Pode usar chroma com um estilo customizado que respeite essas cores.

### 6.10 Aba Describe

- Caixa `bgInset`. Duas colunas: chave (18 colunas, `muted`) e valor (`text`, truncado).
- Campos: Name, Namespace, Node, Status, IP, Controlled By, Image, Restart Count, Requests, Limits, QoS Class, Conditions (`Initialized ✓ Ready ✓ ContainersReady ✓ PodScheduled ✓`), Tolerations.

### 6.11 Aba Events

- Caixa `bgInset`. Cabeçalho em maiúsculas (`muted`): `AGE [6] TYPE [8] REASON [14] MESSAGE [flex]`.
- TYPE: `Normal` em `ok`, `Warning` em `warn`. REASON em `text`. MESSAGE em `textSoft`, truncada.

---

## 7. Imagens 2D (half-block)

### 7.1 Sprite do rack (20 × 24 pixels → 20 colunas × 12 linhas)

Gerado proceduralmente a cada frame (os LEDs dependem do tempo `t`). Para cada pixel `(x, y)`, com `x ∈ [0,19]`, `y ∈ [0,23]`:

```
if y >= 22:                                   # pés
    c = (x in {3,4,15,16}) ? "#2A2C3D" : transparent
elif y == 0 or y == 21 or x == 0 or x == 19:  # contorno externo
    c = "#3A3D55"
elif y == 1 or y == 20 or x == 1 or x == 18:  # moldura
    c = mix(pal.b, "#0B0C12", 0.35)
else:                                          # interior: 3 unidades de 6 linhas
    r = (y-2) % 6 ; u = (y-2) / 6
    r == 0 → "#0D0E15"        (gap entre unidades)
    r == 1 → "#2B2E42"        (bezel)
    r == 4 → "#2B2E42"        (bezel)
    r == 5 → "#1A1C2A"        (sombra)
    r in {2,3}:               (corpo)
        c = "#3A3D55"
        if 8 <= x <= 16:      c = (r == 2) ? "#4A4E6A" : (x%2==0 ? "#22243A" : "#3A3D55")   # baias / ventilação
        if x == 3 and r == 2: c = ok "#5FE3A1"                                               # LED status
        if x == 3 and r == 3: c = Noise(t*3 + u*7) > 0.15 ? pal.a : mix(pal.a, "#0B0C12", 0.7)          # LED energia
        if x == 5 and r == 2: c = Noise(t*7 + u*13 + 1) > 0.45 ? pal.c : mix(pal.c, "#0B0C12", 0.75)   # LED atividade
        if x == 5 and r == 3: c = Noise(t*5 + u*11 + 3) > 0.55 ? pal.c : mix(pal.c, "#0B0C12", 0.75)   # LED atividade
```

`t` é o contador de ticks lógicos (§8). `transparent` = usar `bgPanel` (ou a cor do glow, §6.1).

**Conversão para terminal** (`draw/halfblock.go`):

```go
// px[y][x] em cores; altura par.
for y := 0; y < H; y += 2 {
    for x := 0; x < W; x++ {
        top, bot := px[y][x], px[y+1][x]
        // otimização: se top == bot, use " " com bg = top (ou "█" com fg = top)
        b.WriteString(lipgloss.NewStyle().Foreground(top).Background(bot).Render("▀"))
    }
    b.WriteByte('\n')
}
```

Otimize agrupando células consecutivas com o mesmo par fg/bg num único `Render`. Isso reduz bastante os escapes ANSI.

### 7.2 Heatmap

Ver §6.5. Mesma técnica, com valores vindos da série temporal de CPU de cada pod.

---

## 8. Animações e efeitos

### 8.1 Relógio

- Um único `frameMsg` gerado por `tea.Tick(time.Second/fps, …)`, `fps` padrão **30** (flag `--fps`, máx. 60). O rodapé mostra `bubbletea · {fps} fps` (`faint`), com o fps **medido** (média móvel), não o configurado.
- Mantenha `phase` em segundos (float64, `time.Since(start)`) para efeitos contínuos.
- Mantenha `t` = contador lógico que incrementa a cada **150 ms** (é a cadência do mockup para LEDs, spinner de pods Pending e dados demo). `step = t / 2` (inteiro), usado para rolar séries no modo demo.
- `--no-anim` (ou `p`ausar com `ctrl+p`): para o tick; a tela só redesenha com mensagens de dados.
- Pare o tick quando a janela perder o foco (Bubble Tea v2 reporta focus/blur se habilitado no `View`) e retome no foco.

### 8.2 Gradientes

**Texto (logo):** para o caractere `i` de `n`, posição `p = frac(i/n + phase/6)`. Paradas em `[0, 1/3, 2/3, 1]` → `[a, b, c, a]`. Interpole entre as paradas vizinhas.

**Borda do painel focado:** percorra o perímetro em sentido horário a partir do canto superior esquerdo, com índice `j` de `0` a `P-1` (`P` = número de células da borda). Cor = mesma função do logo com `p = frac(j/P × 1.5 + phase/6)` (o fator 1.5 repete o gradiente para ele "correr" visivelmente). O texto do título embutido fica em `pal.a`, por cima da borda.

### 8.3 Canvas braille

- Cada célula tem 2×4 pontos. Bit do ponto `(dx, dy)`:
  `dx=0: dy0→0x01, dy1→0x02, dy2→0x04, dy3→0x40`; `dx=1: dy0→0x08, dy1→0x10, dy2→0x20, dy3→0x80`. Rune = `0x2800 + bits`.
- Uma cor por célula (a última camada desenhada vence): grade → request → área → linha → ponto final.
- A linha usa Bresenham entre amostras consecutivas mapeadas para coordenadas de ponto.

### 8.4 Pulso

`●` de status "live"/"Ready": cor = `mix(base, bgPanel, 0.65 × (0.5 − 0.5·cos(2π·phase/1.6)))`. O ponto vai de 100% a ~35% e volta em 1.6 s.

### 8.5 Cursor piscando

Bloco `▌` (ou célula com bg `pal.a`) visível quando `int(phase) % 2 == 0` (1 s ligado, 1 s desligado). Na v2, quando o textinput está focado, prefira o cursor real do terminal (`v.Cursor`) com shape de bloco e cor `pal.a`.

### 8.6 Spinner

`⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`, frame = `t % 10`. Usado no header e como glyph de pods Pending/ContainerCreating.

### 8.7 Springs (harmonica)

`harmonica.NewSpring(harmonica.FPS(fps), 6.0, 0.5)` para:

- largura das barras de progresso (memória, CPU/mem do node);
- fade do fundo da linha selecionada (§6.4);
- slide de troca de aba (§6.6).

Quando todos os springs estiverem em repouso (|v| < ε e |x − alvo| < ε) e não houver efeito contínuo visível, reduza o tick para 10 fps.

---

## 9. Modo demo (`--demo`): dados determinísticos

O modo demo precisa reproduzir o mockup exatamente. Use `Noise` de §3.3.

### 9.1 Pods

| #   | Nome | Namespace | Status | Restarts | Idade | CPU base (m) | IP (10.42.0.x) |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 0   | bilibili-raffle-5d8f9c7b4-x2kqp | bilibili-raffle | Running | 0   | 119d | 4   | 23  |
| 1   | bilibili-live-7f6b8d9c5-mn4tz | bilibili-raffle | Running | 2   | 119d | 10  | 24  |
| 2   | lgo-7c5d97584-hq8wd | lgo | Running | 0   | 87d | 6   | 31  |
| 3   | psql-6bc7744f9-r7zlc | ys-odoo | Running | 0   | 64d | 12  | 40  |
| 4   | odoo-7575bf6d8-9wjvk | ys-odoo | Running | 1   | 64d | 18  | 41  |
| 5   | qiandao-1605400800-k8f2m | xcx | Completed | 0   | 2h  | 0   | 52  |
| 6   | lingqu-1605486000-4tq8n | xcx | Completed | 0   | 41m | 0   | 53  |
| 7   | lingqu-1605489600-b7xsd | xcx | Pending | 0   | 8s  | 0   | 54  |
| 8   | coredns-6799fbcd5-w9t4p | kube-system | Running | 0   | 119d | 3   | 2   |
| 9   | metrics-server-67c658944b-lm2x | kube-system | Running | 0   | 119d | 5   | 3   |
| 10  | traefik-f4564c4f4-zq7rn | kube-system | Running | 0   | 119d | 2   | 4   |
| 11  | local-path-provisioner-84db5d-h4 | kube-system | Running | 0   | 119d | 1   | 5   |
| 12  | svclb-traefik-8c2f1-6n2vb | kube-system | Running | 0   | 119d | 1   | 6   |

Selecionado inicial: **#1** (`bilibili-live-7f6b8d9c5-mn4tz`).

CPU "ao vivo" da mini-barra: `max(0, base + round((Noise(t×0.3 + i×9) − 0.5) × 4))` para Running; 0 para os demais.

### 9.2 Deployments

`bilibili-raffle/bilibili-live 1/1`, `bilibili-raffle/bilibili-raffle 1/1`, `ys-odoo/psql 1/1`, `ys-odoo/odoo 1/1`, `lgo/lgo 1/1`, `kube-system/coredns 1/1`, `kube-system/metrics-server 1/1`, `kube-system/traefik 1/1`.

### 9.3 Séries

- **CPU do pod selecionado (m)**: `cpuV(i) = clamp(8 + 2.6·sin(i/4) + 3.4·Noise(i) + (Noise(3i+1) > 0.93 ? 5 : 0), 0.5, 19.5)`. Janela de 60 amostras: `i = step … step+59`. Valor atual = `round(cpuV(step+59))`.
- **Memória (Mi)**: `memV(i) = 73 + 3·sin(i/9) + 2·Noise(2i)`. Sparkline com 34 amostras de `memV − 66` em escala 0-12. Atual = `round(memV(step+33))`. Limite = 128Mi; percentual = atual/128.
- **Rede**: `rx(i) = Noise(2.3i)·10`, `tx(i) = Noise(4.1i + 5)·6` (kB/s). Sparklines de 34 amostras, escalas 10 e 6. Valores exibidos com 1 casa decimal.
- **Heatmap** (pods 0, 1, 2, 3, 4, 8, 9, 10), coluna `col`, linha `row`: `k = clamp((cpuBase/20)·0.9 + (Noise((step+col)·1.7 + row·31) − 0.4)·0.45, 0, 1)`.
- **CPU do node (%)**: `round(18 + Noise(step)·8)`.
- Sparkline: índice `round(v/max × 7)` limitado a [0, 7] em `▁▂▃▄▅▆▇█`.

### 9.4 Logs (ciclo de 12 linhas; avança 1 linha a cada 8 ticks)

```
INFO  ws connected room=21452505 shard=3
DEBUG heartbeat ok rtt=42ms
INFO  danmaku batch received count=38
INFO  raffle detected id=883104 type=gift
INFO  join raffle ok id=883104 latency=118ms
WARN  rate limited by upstream, backoff 2s
DEBUG heartbeat ok rtt=39ms
INFO  danmaku batch received count=51
INFO  raffle detected id=883117 type=guard
ERROR join raffle failed id=883117: already ended
INFO  reconnecting ws in 1s
INFO  ws connected room=21452505 shard=3
```

Timestamp da linha `j`: segundos `s = (14·60 + 3j) mod 3600` → `20:MM:SS`.

### 9.5 Events

| Age | Type | Reason | Message |
| --- | --- | --- | --- |
| 8s  | Normal | Scheduled | Successfully assigned xcx/lingqu-1605489600-b7xsd |
| 7s  | Normal | Pulling | Pulling image "registry.local/xcx:1.4.2" |
| 41m | Normal | Completed | Job completed: lingqu-1605486000 |
| 3d  | Warning | BackOff | Back-off restarting failed container in bilibili-live |
| 3d  | Normal | Started | Started container app |
| 12d | Warning | OOMKilled | Container odoo exceeded memory limit |
| 12d | Normal | Created | Created container odoo |

### 9.6 Config (YAML) e Describe

YAML gerado a partir do pod selecionado:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: <nome>
  namespace: <ns>
  labels:
    app: <2 primeiros segmentos do nome>
spec:
  containers:
  - name: app
    image: registry.local/<ns>:1.4.2
    resources:
      requests: { cpu: 10m, memory: 64Mi }
      limits: { cpu: 50m, memory: 128Mi }
    env:
    - name: LOG_LEVEL
      value: info
  restartPolicy: Always
  nodeName: huawei-k3s-01
```

Describe: Node `huawei-k3s-01/192.168.0.12`, Controlled By `ReplicaSet/<nome sem o último segmento>`, QoS `Burstable`, Tolerations `node.kubernetes.io/not-ready:NoExecute for 300s`, e o restante a partir do pod.

### 9.7 Header/node demo

Contexto `huawei_k3s`; `k8s v1.28.4+k3s2`; `nodes 1`; `ns 8`; `pods 13`; `uptime 119d`; node `huawei-k3s-01`, `Ready`, `mem 1.4Gi / 3.8Gi` (37%), `pods 13 / 110`, `arch amd64`.

---

## 10. Modo real (client-go)

- `data.Source` é uma interface com dois impls (`demo`, `kube`). A UI só conhece mensagens.
- **Informers** (`SharedInformerFactory`, resync 30 s): Pods, Deployments, Namespaces, Events, Nodes, Services. Os handlers fazem `program.Send(podsMsg{…})` (snapshot imutável). **Nunca** mutar o Model fora do `Update`.
- **Métricas**: `metricsv1beta1` (`k8s.io/metrics/pkg/client/clientset/versioned`), polling a cada **5 s** de `PodMetricses` e `NodeMetricses`. Guarde um ring buffer de 120 amostras por pod (CPU em milicores, memória em Mi). O heatmap usa as últimas `N` amostras; o gráfico usa as últimas 60. Se o metrics-server não existir, o header mostra `● stale` e os gráficos exibem `metrics-server indisponível` centralizado (`muted`).
- **Rede**: o metrics.k8s.io não expõe rede. Use o endpoint de stats summary do kubelet via proxy da API (`/api/v1/nodes/<node>/proxy/stats/summary`), calculando a taxa por diferença entre amostras. Se falhar, mostre `n/a` no card Network.
- **Logs**: `PodLogs(... Follow: true, TailLines: 500)` numa goroutine, lendo por linha e enviando `logLineMsg`. Cancele o contexto ao trocar de pod. Detecte o nível por regex (`INFO|DEBUG|WARN|WARNING|ERROR|ERR|FATAL`). Se não encontrar, use `INFO`. Timestamp: `Timestamps: true` e formate `HH:MM:SS`.
- **Requests/limits** do gráfico: vêm do spec do container (o primeiro, ou a soma).
- **Ações**:
  - `s`: shell. Suspenda a TUI com `tea.ExecProcess` rodando `kubectl exec -it <pod> -n <ns> -- sh`.
  - `ctrl+d`: delete com confirmação (modal centralizado, borda gradiente, `y/N`).
  - `ctrl+k`: trocar contexto (lista em modal).

---

## 11. Teclado e mouse

| Tecla | Ação |
| --- | --- |
| `↑/↓`, `k/j` | mover seleção no painel focado |
| `tab` / `shift+tab` | próximo / anterior painel focável (Namespaces → Deployments → Pods → Detalhes) |
| `/` | focar filtro de pods |
| `esc` | sair do filtro / fechar modal / limpar filtro de namespace |
| `l` `c` `t` `d` `e` | abas Log, Config, Top, Describe, Events |
| `[` `]` | aba anterior / próxima |
| `s` | shell no pod |
| `ctrl+d` | deletar pod (confirmação) |
| `ctrl+k` | trocar contexto |
| `p` | ciclar paleta |
| `1` | mostrar/esconder coluna esquerda |
| `ctrl+p` | pausar/retomar animações |
| `?` | alternar help curto/completo |
| `q`, `ctrl+c` | sair |
| `pgup/pgdn`, `home/end` | rolar viewport (Log/Config) |

**Rodapé (help curto)**, como keycaps: cada tecla com fundo `borderFaint`, texto `text`, padding 1, seguida da descrição em `muted`, 2 espaços entre pares:
`↑↓ move tab panel / filter l logs s shell d describe ctrl+d delete ? help q quit`.
À direita: `bubbletea · 30 fps` em `faint`.

O help completo (`?`) abre um overlay centralizado (`lipgloss.Place`) com todas as teclas em 2 colunas, borda com gradiente.

**Mouse** (`v.MouseMode = tea.MouseModeCellMotion`): clique em linha de pod/namespace/deployment seleciona; clique em aba troca; clique em painel foca; roda rola o painel sob o ponteiro; motion faz hover nas linhas de pods.

---

## 12. Arquitetura Bubble Tea

```go
type Model struct {
    w, h      int
    focus     Focus          // FocusNamespaces | FocusDeploys | FocusPods | FocusDetails
    tab       Tab            // TabLog | TabConfig | TabTop | TabDescribe | TabEvents
    pal       theme.Palette
    clock     anim.Clock     // phase, t, step, fps medido
    springs   anim.Springs
    filter    textinput.Model
    logs      viewport.Model
    yaml      viewport.Model
    help      help.Model
    keys      KeyMap
    data      Snapshot       // pods, deploys, ns, events, node, séries
    selPod    int
    selNS     int
    hoverRow  int
    modal     *Modal
    src       data.Source
}
```

- `Init()`: `tea.Batch(tick(), src.Start())`.
- `Update`: `tea.WindowSizeMsg` → recalcula layout; `frameMsg` → avança relógio e springs e reagenda o tick; mensagens de dados → substituem o snapshot; teclas/mouse → §11.
- `View()`: monta com `lipgloss.JoinVertical(header, "", JoinHorizontal(left, " ", center, " ", right), divider, footer)` e retorna `tea.NewView(s)` com `AltScreen = true`, `MouseMode = CellMotion` e `ReportFocus = true` (ou os campos equivalentes da versão resolvida).
- Cada painel é uma função pura `Render(state, w, h) string`, fácil de testar com golden files.

---

## 13. Performance

- Com 160×48 e efeitos ligados, o `View()` deve ficar **abaixo de 4 ms** (meça com um benchmark `go test -bench`).
- Agrupe runs de mesma cor (§7.1). Cacheie estilos lipgloss por cor (map `color → Style`).
- O sprite só muda nos LEDs: cacheie as linhas sem LED.
- O heatmap e o gráfico só mudam quando chega amostra nova (demo: a cada 2 ticks; real: a cada 5 s). Cacheie a string renderizada e invalide por versão dos dados ou da paleta.
- A borda gradiente e o logo mudam todo frame, mas são baratos (perímetro + 8 runes).

---

## 14. Critérios de aceite

1. `go run ./cmd/lazykube --demo` num terminal 160×48 truecolor reproduz o layout de §4 com os dados de §9 e as cores de §3.
2. O painel Pods inicia focado com borda em gradiente **em movimento**. Ao trocar o foco (Tab), a borda gradiente migra para o novo painel e o antigo volta para `border`.
3. O logo `lazykube` tem gradiente animado `a→b→c→a` com ciclo de ~6 s.
4. O sprite do rack aparece em half-blocks, com contorno, moldura em `b` escurecido, 3 unidades, baias, e LEDs `c`/`a` piscando de forma irregular.
5. O heatmap mostra 8 pods com escala `#161827 → b → a` e rola para a esquerda.
6. A aba Top mostra o gráfico braille com área em gradiente vertical, linha em gradiente horizontal `b→c`, linha `╌` de request em 10m com rótulo, e ponto final `●`.
7. A barra de memória anima a largura com spring; as sparklines atualizam.
8. Spinner braille no header e no pod Pending; `●` live/Ready pulsando; cursor do filtro piscando.
9. Clique numa linha de pod seleciona com fade de fundo; clique nas abas troca; `/` filtra com fuzzy.
10. `p` alterna `charm → ocean → matrix` e **tudo** que usa `a/b/c` muda junto.
11. `--no-anim` congela os efeitos sem quebrar nada; a CPU em idle fica perto de 0%.
12. Abaixo de 120×36 aparece a mensagem de tamanho mínimo; entre 120 e 140 colunas a coluna esquerda some.
13. Sem emoji e sem cores fora dos tokens de §3.
14. Testes: golden files de cada painel em `--demo` com `t` fixo (ex.: `t=40`), e benchmark do `View()`.
15. Modo real funciona contra um cluster k3s com metrics-server; sem metrics-server, degrada para `stale` e mensagens `n/a`, sem panics.
