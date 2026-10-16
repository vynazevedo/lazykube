<!-- ANSI Shadow Font -->
```
 ██╗      █████╗ ███████╗██╗   ██╗██╗  ██╗██╗   ██╗██████╗ ███████╗
 ██║     ██╔══██╗╚══███╔╝╚██╗ ██╔╝██║ ██╔╝██║   ██║██╔══██╗██╔════╝
 ██║     ███████║  ███╔╝  ╚████╔╝ █████╔╝ ██║   ██║██████╔╝█████╗
 ██║     ██╔══██║ ███╔╝    ╚██╔╝  ██╔═██╗ ██║   ██║██╔══██╗██╔══╝
 ███████╗██║  ██║███████╗   ██║   ██║  ██╗╚██████╔╝██████╔╝███████╗
 ╚══════╝╚═╝  ╚═╝╚══════╝   ╚═╝   ╚═╝  ╚═╝ ╚═════╝ ╚═════╝ ╚══════╝
```

<div align="center">

```
 ┌──────────────────────────────────────────────────────────────┐
 │   kubectl get pods, mas com contexto, métricas e logs        │
 │   na mesma tela.                                             │
 └──────────────────────────────────────────────────────────────┘
```

[![CI](https://github.com/vynazevedo/lazykube/actions/workflows/ci.yaml/badge.svg)](https://github.com/vynazevedo/lazykube/actions/workflows/ci.yaml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![GitHub stars](https://img.shields.io/github/stars/vynazevedo/lazykube?style=social)](https://github.com/vynazevedo/lazykube/stargazers)

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev/)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-326CE5?style=for-the-badge&logo=kubernetes&logoColor=white)](https://kubernetes.io/)
[![Bubble Tea](https://img.shields.io/badge/Bubble_Tea-v2-FF5F87?style=for-the-badge)](https://github.com/charmbracelet/bubbletea)
[![Linux](https://img.shields.io/badge/Linux-FCC624?style=for-the-badge&logo=linux&logoColor=black)](https://www.linux.org/)

**A fast terminal UI to watch your Kubernetes cluster.** (EN)

**Uma TUI rápida para acompanhar seu cluster Kubernetes pelo terminal.** (PT-BR)

Pods, deployments, namespaces, node, CPU, memória, rede, logs, eventos e YAML
em um único layout, atualizado em tempo real por informers e com animações
suaves. Funciona com qualquer contexto do seu kubeconfig e degrada com
elegância quando falta permissão ou metrics-server.

*Somente leitura: o lazykube observa o cluster e nunca altera nada.*

---

![lazykube](assets/lazykube.png)

</div>

```
 ╔═══════════════════════════════════════════════════════╗
 ║                    T A B L E                          ║
 ║                 O F  C O N T E N T S                  ║
 ╠═══════════════════════════════════════════════════════╣
 ║                                                       ║
 ║  [0x01] .... Quick Install                            ║
 ║  [0x02] .... Uso                                      ║
 ║  [0x03] .... Layout                                   ║
 ║  [0x04] .... Atalhos                                  ║
 ║  [0x05] .... Métricas e permissões                    ║
 ║  [0x06] .... Estrutura do projeto                     ║
 ║  [0x07] .... Compatibilidade                          ║
 ║  [0x08] .... Contribuindo                             ║
 ║                                                       ║
 ╚═══════════════════════════════════════════════════════╝
```

---

## `[0x01]` Quick Install

Requer Go 1.26+ e um kubeconfig válido.

```bash
git clone https://github.com/vynazevedo/lazykube.git
cd lazykube
go build -o lazykube ./cmd/lazykube
sudo install -m 0755 lazykube /usr/local/bin/lazykube
```

Para ver a interface sem cluster nenhum, use o modo demo:

```bash
lazykube --demo
```

---

## `[0x02]` Uso

```
 ┌─ FLAGS ──────────────────────────────────────────────────────────────┐
 │                                                                      │
 │  lazykube                        # contexto atual do kubeconfig      │
 │  lazykube --context staging      # escolhe o contexto                │
 │  lazykube -n apps                # limita a um namespace             │
 │  lazykube --kubeconfig ~/k.yaml  # kubeconfig alternativo            │
 │  lazykube --palette ocean        # paleta: charm, ocean ou matrix    │
 │  lazykube --fps 30               # quadros por segundo (1 a 60)      │
 │  lazykube --no-anim              # desliga animações                 │
 │  lazykube --demo                 # dados sintéticos determinísticos  │
 │                                                                      │
 └──────────────────────────────────────────────────────────────────────┘
```

Ao iniciar, uma tela de loading animada mostra cada etapa da conexão:
kubeconfig, contexto e versão do servidor, permissões e sincronização do
cache. Se uma etapa falhar, a tela aponta qual foi e por quê; `q` sai e
`ctrl+c` cancela a conexão a qualquer momento.

O lazykube só lê o cluster: lista e observa recursos, segue logs e consulta
métricas. Nenhuma ação de escrita é executada.

---

## `[0x03]` Layout

```
 ┌─ ESQUERDA ──────────┬─ CENTRO ─────────────────┬─ DIREITA ──────────────┐
 │                     │                          │                        │
 │  Node               │  Pods (foco, filtro /)   │  Log      tail -f      │
 │   cpu, mem, pods    │   status, restarts,      │  Config   YAML         │
 │  Namespaces         │   idade, cpu ao vivo     │  Top      cpu, mem,    │
 │   pods por ns       │                          │           rede, logs   │
 │  Deployments        │  CPU heatmap             │  Describe              │
 │   ready / desired   │   pods mais ativos       │  Events                │
 │                     │                          │                        │
 └─────────────────────┴──────────────────────────┴────────────────────────┘
```

Abaixo de 140 colunas a coluna esquerda é recolhida automaticamente; a
tecla `1` alterna manualmente em qualquer largura. O mouse funciona em todos
os painéis: clique seleciona, a roda rola e o clique na aba troca de aba.

---

## `[0x04]` Atalhos

```
 ┌─ NAVEGAÇÃO ───────────────────────────────────────────┐
 │                                                       │
 │  ↑ ↓  /  k j       move a seleção                     │
 │  tab / shift+tab   próximo / anterior painel          │
 │  /                 filtra pods (nome e namespace)     │
 │  enter             filtra pods pelo namespace         │
 │  esc               limpa filtros                      │
 │  1                 mostra / esconde coluna esquerda   │
 │                                                       │
 ├─ DETALHES ────────────────────────────────────────────┤
 │                                                       │
 │  l  c  t  d  e     Log, Config, Top, Describe, Events │
 │  [  ]              aba anterior / próxima             │
 │  f                 liga / desliga follow dos logs     │
 │  w                 quebra de linha                    │
 │  g  G              topo / fim                         │
 │  pgup  pgdn        página                             │
 │                                                       │
 ├─ GERAL ───────────────────────────────────────────────┤
 │                                                       │
 │  p                 troca a paleta de cores            │
 │  ctrl+p            pausa / retoma animações           │
 │  ?                 ajuda                              │
 │  q  ctrl+c         sair                               │
 │                                                       │
 └───────────────────────────────────────────────────────┘
```

---

## `[0x05]` Métricas e permissões

Os gráficos são montados a partir do metrics-server e do kubelet. Nada é
inventado: o histórico cresce a cada nova amostra (resolução padrão de 15s)
e, enquanto a janela enche, o painel mostra `coletando 12/60`.

```
 RECURSO                         USO                        SEM PERMISSÃO
 ───────                         ───                        ─────────────
 pods, deployments (list/watch)  base da interface          não inicia
 events (list/watch)             aba Events                 aviso na aba
 namespaces (list/watch)         painel Namespaces          deriva dos pods
 nodes (list/watch)              painel Node                contagem pelos pods
 metrics.k8s.io pods, nodes      CPU, memória, heatmap      motivo no painel
 nodes/proxy (get)               rede RX/TX                 motivo no card
```

Quando algo falta, o painel diz o motivo em vez de ficar vazio, por exemplo
`metrics-server não instalado`, `sem permissão: nodes/proxy` ou
`obsoleto: kubelet indisponível`.

---

## `[0x06]` Estrutura do projeto

```
lazykube/
├── cmd/lazykube/        # entrada, flags e conexao
├── internal/
│   ├── app/             # modelo Bubble Tea, layout, teclas, mouse, loading
│   ├── ui/              # painéis: node, pods, heatmap, detalhes, loading
│   ├── kube/            # informers, logs, metrics-server e kubelet stats
│   ├── data/            # contrato de dados e fonte demo
│   ├── draw/            # canvas, braille, larguras de grafema
│   ├── anim/            # relógio, springs e fades
│   └── theme/           # paletas e gradientes
├── pkg/                 # utilitários herdados
└── assets/              # imagens do README
```

---

## `[0x07]` Compatibilidade

```
 PLATAFORMA          STATUS
 ──────────          ──────
 Linux (amd64)       testado
 WSL2 (Ubuntu)       testado
 Linux (arm64)       não testado
 macOS               não testado
```

O acesso ao cluster usa `client-go` v0.36 e funciona com qualquer
distribuição que exponha a API padrão do Kubernetes (k3s, kind, GKE, EKS,
AKS). Os gráficos de CPU e memória dependem do metrics-server instalado.

Use um terminal com true color e uma fonte com caracteres braille e de bloco
(JetBrainsMono, Fira Code, Cascadia). No iTerm2, habilite *mouse reporting*
para usar o mouse.

---

## `[0x08]` Contribuindo

```
 1. Fork o repo
 2. Crie uma branch (git switch -c minha-feature)
 3. Commit (git commit -m "feat: minha feature")
 4. Push (git push origin minha-feature)
 5. Abre um PR
```

Validação local, a mesma do CI:

```bash
go vet ./...
go test -race -count=1 ./...
golangci-lint run ./...
```

Os testes usam `client-go` fake e goldens de tela; nenhum cluster real é
necessário. Para atualizar os goldens após uma mudança visual intencional:
`go test ./internal/app ./internal/ui -update`.

---

<div align="center">

```
 ┌──────────────────────────────────────────────────────────────┐
 │   Fork do TNK-Studio/lazykube, reescrito com Bubble Tea v2.  │
 └──────────────────────────────────────────────────────────────┘
```

**[@vynazevedo](https://github.com/vynazevedo)**

</div>
