# Modernizacao da base do lazykube

Este documento registra as mudancas da branch `feat/fundacao`, as versoes finais escolhidas e as mudancas incompativeis (breaking changes) com o caminho de migracao.

## Versoes finais

| Componente | Antes | Depois |
| --- | --- | --- |
| Diretiva `go` | 1.16 | 1.26 |
| k8s.io/api, apimachinery, client-go, cli-runtime, kubectl, metrics | v0.19.3 | v0.36.5 |
| k8s.io/klog/v2 | v2.2.0 | v2.140.0 |
| Biblioteca de TUI | fork TNK-Studio/gocui + nsf/termbox-go | github.com/jesseduffield/gocui v0.3.1-0.20251223152224-58dd9c27b794 (tcell v2.8.0) |
| github.com/go-logr/logr | v0.2.0 | v1.4.4 |
| github.com/spf13/cobra | v1.0.0 | v1.10.2 |
| golang.org/x/net | v0.0.0-2020 | v0.60.0 |
| golang.org/x/text | v0.3.3 | v0.42.0 |

Removidos: `replace` de golang.org/x/sys, `replace` do fork do gocui, `replace` e dependencia de Matt-Gleich/release, nsf/termbox-go, docker/distribution, uso direto de github.com/pkg/errors e de `io/ioutil`.

## Por que Go 1.26 e nao 1.24

O pedido inicial era Go 1.24. A decisao foi revista para 1.26 por dois motivos:

1. Go 1.24 esta sem suporte upstream desde fevereiro de 2026. Compilado com 1.24.x, o govulncheck aponta vulnerabilidades da biblioteca padrao que nao recebem mais correcao nessa linha.
2. Com a diretiva `go 1.24`, as versoes mais novas permitidas eram k8s.io/* v0.34.10, golang.org/x/net v0.50.0 e golang.org/x/text v0.34.0. Nessas versoes o govulncheck acusava 9 vulnerabilidades alcancaveis pelo codigo do lazykube: 8 em golang.org/x/net (HTTP/2 e idna, corrigidas entre v0.51.0 e v0.60.0) e 1 em golang.org/x/text (corrigida em v0.39.0). Todas as versoes corrigidas exigem `go 1.25` ou superior.

Subir para 1.26 permite usar k8s.io/* v0.36.x e as versoes corrigidas de x/net e x/text, e torna o govulncheck um gate verde no CI.

O `go.mod` nao fixa diretiva `toolchain`. Maquinas com Go mais antigo instalado baixam automaticamente a toolchain exigida quando `GOTOOLCHAIN=auto` (o padrao do Go desde 1.21). Ambientes com `GOTOOLCHAIN=local` precisam ter Go 1.26 ou superior instalado.

## Mudancas incompativeis e migracao

### Plugins de autenticacao azure, gcp e openstack

O client-go removeu os plugins embutidos `azure`, `gcp` e `openstack`. O lazykube agora registra apenas `exec` e `oidc`.

Migracao: usar o plugin exec do provedor no kubeconfig.

- GKE: instalar `gke-gcloud-auth-plugin` e regerar o kubeconfig com `gcloud container clusters get-credentials`.
- AKS: instalar `kubelogin` e executar `kubelogin convert-kubeconfig`.
- OpenStack: usar o plugin exec `client-keystone-auth`.

### Cores no config.yaml

As cores de `gui_config` (`fg_color`, `bg_color`, `sel_fg_color`, `sel_bg_color`) passaram a ser gravadas por nome: `default`, `black`, `red`, `green`, `yellow`, `blue`, `magenta`, `cyan`, `white`.

Compatibilidade: arquivos existentes com os inteiros antigos do termbox (0 a 8) continuam sendo lidos e convertidos para o nome correspondente. Valores desconhecidos caem em `default` sem erro, para que um valor invalido nao faca o lazykube sobrescrever o config do usuario com o padrao. Nenhuma acao e necessaria.

### Permissao do config.yaml

Arquivos novos de config sao criados com permissao `0600` (antes `0755`). Arquivos existentes mantem a permissao atual.

### Comando interativo (exec e run)

Ao final de `Execute the command` e `Run a pod with an image`, a mensagem agora e "Press Enter to return to lazykube." (antes "Press 'x' twice time return to lazykube."). Com o termbox o Enter nao funcionava; com tcell a TUI e suspensa de verdade durante o comando, e o Enter volta a funcionar.

### Verificacao de nova versao

O verificador de release (antes Matt-Gleich/release) foi substituido por `pkg/release`:

- timeout de 3 segundos e resposta limitada a 1 MiB;
- comparacao semver quando as duas versoes sao validas (antes qualquer diferenca de texto contava como desatualizada, inclusive uma versao local mais nova);
- quando alguma versao nao e semver (por exemplo builds locais "No Version Provided"), mantem o comportamento antigo de comparar o texto.

A mensagem de nova versao nao usa mais emoji.

### API interna pkg/gui

Para quem estende `pkg/gui`:

- `View.SetOrigin`, `View.SetCursor` e `View.ResetCursorOrigin` nao retornam mais erro. O gocui novo limita a origem a valores nao negativos em vez de falhar.
- `View.MoveCursor(dx, dy, writeMode)` foi substituido por `View.MoveCursorVertically(dy)`, que reproduz a regra antiga de rolagem vertical (`NextVerticalPosition`, coberta por testes). Para preencher um campo editavel use `View.SetEditableContent(s)`.
- `View.MouseDisable` foi removido. Os dois unicos usos eram views editaveis, e no gocui novo o clique em view editavel ja posiciona o cursor dentro do texto.
- `View.OnCursorChange` foi removido (nao tinha usos).
- `View.Size()` continua retornando a area interna, como no fork antigo (no gocui novo, `Size()` retorna a area externa).
- `View.Line()` remove as celulas `\x00` de preenchimento que o gocui novo deixa no buffer, como `ViewBufferLines()` ja fazia.
- `Gui.ForceFlush` e `Gui.ReInitTermBox` foram substituidos por `Gui.Suspend`, `Gui.Resume` e `Gui.RunSuspended(func() error) error`.
- Novo `NewHeadlessGui(config, largura, altura, views...)` para testes.
- `gocui.Screen` e global: so pode existir um `Gui` por processo. Testes headless nao podem usar `t.Parallel` nem criar mais de um `Gui` no mesmo binario de teste. Por isso o teste de ciclo de vida do `Gui` nao suporta `go test -count` maior que 1; o CI usa `-count=1`.
- `gui.StateKeyError` foi renomeado para `gui.ErrStateKey` (convencao `ErrXxx` exigida pelo staticcheck). Migracao: trocar o nome nas chamadas a `errors.Is`.
- `kubecli.GetPodRawMetrics` e `kubecli.GetPodMetrics` recebem `labels.Selector` (k8s.io/apimachinery/pkg/labels) em vez do tipo exposto pelo antigo dot import. O tipo e o mesmo; so muda a forma de referencia no codigo chamador.

### Logger do klog

`KLogger` (logr v0.2) foi substituido por `KLogSink`, que implementa `logr.LogSink` v1. Agora o log inclui os pares chave-valor, o nome do logger e o erro (antes so a mensagem).

Seguranca (achado da revisao): o client-go registra corpos completos de requisicao e resposta, inclusive Secrets, nos niveis V(8) e V(10). O `KLogSink` aceita apenas o nivel 0 por padrao (`DefaultKLogVerbosity`; `NewKLogSinkWithVerbosity` para depuracao explicita). Mesmo com verbosidade maior, mensagens de corpo (`Response Body`, `Request Body`, `curl -`) sao substituidas por `[REDACTED]`, assim como campos cujo nome contem `authorization`, `token`, `password`, `secret`, `credential`, `cookie`, `body`, `data`, `cert` ou `key`. Coberto por testes com uma resposta sintetica de Secret.

### Teclas exibidas na barra de opcoes

`utils.GetKey` comparava codigos numericos do termbox. Com tcell, setas, PgUp/PgDn, Tab e teclas F apareceriam como caracteres errados. A funcao agora usa uma tabela tipada com as constantes do gocui.

## Correcao de desempenho: laco de renderizacao

`OnRenderOptions` roda em todo layout e chamava `Gui.RenderString`, que enfileira um `g.Update`. Cada update gerava um novo layout, e o laco principal nunca ficava ocioso. O codigo antigo ja tinha esse laco; com termbox ele aparecia menos. Medido com o tcell: cerca de 5.500 quadros completos e 3,7 MB/s de saida no terminal com a TUI parada.

Correcao:

- `RenderString` nao enfileira update quando a view ja mostra exatamente o mesmo conteudo (cache invalidado em `Clear`, `Write` e `SetViewContent`);
- `Gui.Run` inicia um ticker de atualizacao (`DefaultRefreshInterval`, 250 ms, configuravel em `Gui.RefreshInterval`) que mantem os paineis com `reRenderInterval` (3 s) e o streaming de logs atualizados sem laco ocupado.
- o ticker mantem no maximo um update pendente. Enquanto o laco principal esta bloqueado (exec, edit e run rodam dentro do handler), os ticks seguintes sao descartados em vez de acumular goroutines na fila de 20 eventos do gocui. `Gui.Suspend` pausa o ticker e `Gui.Resume` o retoma (achado da revisao).

Resultado medido: saida ociosa caiu de cerca de 3,7 MB/s para cerca de 19 KB/s.

## CI

- `ci.yaml`: matriz Go 1.26.x e stable com verificacao de `go mod tidy`, build, vet e `go test -race`; golangci-lint v2 via golangci-lint-action v9; govulncheck como gate.
- `release.yml`: o `izumin5210/action-go-crossbuild` (imagem com Go antigo, incapaz de compilar o modulo atual) foi substituido por `goxz` executado diretamente, mantendo o padrao de nomes dos artefatos usado pelo passo do homebrew tap. A tag vem de `github.ref_name` em vez de `dawidd6/action-get-tag`.
- golint e goreportcard foram removidos (golint esta descontinuado; o golangci-lint cobre a analise estatica).

## Limpeza para o golangci-lint

O CI passou a exigir golangci-lint v2 (`linters.default: standard`). Os 27 achados da base antiga foram corrigidos sem mudar comportamento: erros nao verificados (`clipboard.WriteAll`, `view.SetState`), atribuicoes inefetivas, mensagens de erro em minusculas, `strings.ReplaceAll`, remocao de dot import, condicao Yoda, seletores de campo embutido e a variavel de erro sem uso `noNamespaceSelectedErr`.

### Pendencia: layout para terminais baixos

Em `pkg/app/style.go`, `cacheAbleReactiveHeight` e `cacheAbleMigrateTopFunc` calculavam valores especiais para telas menores que `heightBoundary()`, mas sobrescreviam o resultado logo em seguida. Na pratica esse ramo nunca teve efeito: a altura sempre e a padrao e `migrateTop` sempre retorna 1. O codigo foi reescrito para expressar exatamente o comportamento atual (constante `defaultMigrateTop`). Implementar de fato o layout para terminais baixos e uma mudanca de comportamento e fica para uma entrega propria.

## Comentarios no codigo

Seguindo a regra do projeto de nao manter comentarios no codigo, os comentarios de linha dos arquivos tocados por esta branch foram removidos (145 linhas, quase todas do tipo `// Nome Nome`). Foram mantidos os cabecalhos de licenca e as diretivas `//nolint`; as oito copias repetidas de `//nolint:gocognit` em `clusterinfo.go` viraram uma so.

## Testes adicionados

- `pkg/config`: leitura de cores legadas e por nome, round trip YAML e mapeamento para `gocui.Attribute`.
- `pkg/gui`: `NextVerticalPosition`, cache de conteudo renderizado, refresher (coalescencia com laco bloqueado, pausa, encerramento sem vazamento de goroutines) e ciclo de vida headless (render, ticker de atualizacao, selecao de linha, encerramento).
- `pkg/kubecli`: `KLogSink` (campos, nome, erro, imutabilidade de `WithValues`, filtro de verbosidade e redacao de Secrets e credenciais).
- `pkg/release`: parse de URL, comparacao de versao e cliente HTTP com `httptest`.
- `pkg/utils`: `GetKey`.

## Smoke test da TUI

Executado em pty com timeout, `HOME` isolado e um kubeconfig falso apontando para `https://127.0.0.1:1` (inalcancavel), sem contato com clusters reais: a TUI renderiza os paineis, navega (Tab, j, seta para baixo), abre o menu de acoes, fecha com Esc e encerra com Ctrl+C com codigo 0, sem panic.

Limitacao: `utils.Home()` resolve a home via `os/user`, entao o smoke test e os testes leem `~/.lazykube/config.yaml` real e gravam logs em `~/.lazykube/log` mesmo com `HOME` alterado. O config real foi lido sem ser reescrito.

Nao verificado sem cluster: os fluxos que suspendem a TUI (exec, run e edit) dependem de recursos reais e devem ser validados com kind ou k3d.
