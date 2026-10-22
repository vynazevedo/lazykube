# Contribuindo com o lazykube

Consulte o [código de conduta](CODE_OF_CONDUCT.md) antes de participar.
Bugs e propostas vão para os templates de issue. Vulnerabilidades devem seguir
[SECURITY.md](SECURITY.md), sem detalhes em issues públicas.

## Ambiente e escopo

Use Go 1.26 com o patch mais recente. Compile com
`go build -o lazykube ./cmd/lazykube` e explore com
`./lazykube --demo --no-anim`. Não são necessários cluster ou kubeconfig.

Os testes devem usar client-go fake, servidores HTTP sintéticos ou envtest local.
Nunca use clusters reais, kind ou o kubeconfig pessoal. Os testes de integração
criam dois servidores locais e kubeconfigs descartáveis em `t.TempDir`.
Não conceda `nodes/proxy`. Dados de incidentes, tokens e nomes privados não devem
entrar em fixtures, goldens, logs ou capturas de tela.

A política planejada para ações na versão 1.0 exige `--read-write` por processo,
sem persistência. Contextos protegidos sempre prevalecem e nenhuma tecla remove
a política. A edição se limita a recursos existentes, com dry-run server-side;
criação fica fora da 1.0. O audit JSONL deve ser privado, sem payload ou comandos,
com até 10 arquivos de 10 MiB e retenção de 30 dias. Falhas do audit bloqueiam ações.
Exec e port-forward devem compartilhar um único transporte. WebSocket depende
de validação nos testes e não deve ter fallback automático.
Esses requisitos não significam que as funcionalidades já estejam implementadas.

## Validação

Execute o gate nesta ordem, avançando apenas quando a etapa anterior passar:

```bash
gofmt -l $(git ls-files '*.go')
go vet ./...
KUBECONFIG=/dev/null go test -race -count=1 ./...
golangci-lint run --timeout=10m
go mod tidy
git diff --exit-code -- go.mod go.sum
```

`gofmt -l` deve produzir saída vazia. Use golangci-lint 2.13.2 com Go 1.26.x.
Para scripts, execute `bash -n install.sh scripts/test-install.sh`,
`shellcheck install.sh scripts/test-install.sh` e `bash scripts/test-install.sh`.
Os testes do instalador precisam abrir um servidor HTTP em localhost.

A integração é opt-in localmente, com a build tag `integration`:

```bash
bash scripts/test-integration.sh 1.36.2
```

O script baixa somente kube-apiserver, etcd e kubectl de envtest. Não inicia
containers ou workloads. O CI executa o mesmo teste em 1.34.1, 1.35.0 e 1.36.2.
Isso valida conexão, leitura e isolamento entre contextos, não execução de pods,
exec, port-forward, metrics-server ou todas as funcionalidades de um cluster.

Para validar o pacote, instale GoReleaser 2.18.3 e Syft 1.55.0 no PATH e execute:

```bash
goreleaser check
goreleaser release --snapshot --clean
```

Snapshots não publicam releases nem fazem assinatura keyless. A assinatura real
usa OIDC do GitHub no workflow de release após o gate completo no SHA da tag.
O GoReleaser prepara uma release em draft; o workflow só a publica depois
de anexar o bundle de assinatura dos checksums.

## Pull requests

Abra uma mudança com escopo claro, descrevendo problema, comportamento resultante,
testes e limitações. Não adicione comentários no código, travessões ou emojis.
Atualizações de goldens devem corresponder a mudanças visuais intencionais.
Use commits Conventional Commits em português, como
`fix: corrige seleção de pods`, sem trailers. Mantenha a identidade Git configurada.
Não altere a política de permissões nem habilite publicação externa incidentalmente.

Preserve [LICENSE](LICENSE), [NOTICE](NOTICE) e os créditos de origem. Contribuições
são recebidas sob Apache-2.0. Os mantenedores revisam o escopo e a validação antes
do merge; uma aprovação não publica automaticamente uma release.

## Canais de distribuição propostos

Nenhum tap ou repositório de pacotes externo é publicado por este workflow.
As propostas abaixo dependem de decisão dos mantenedores:

| Opção | Benefício | Trabalho e decisão pendentes |
| --- | --- | --- |
| Homebrew tap | Atualização por `brew` em macOS/Linux | Definir proprietário e repositório do tap, política de atualização e credencial entre repositórios; testar em plataformas nativas |
| Pacotes deb/rpm com nFPM | Instalação e remoção pelo gerenciador do Linux | Definir mantenedor e metadados, instalar binário e LICENSE/NOTICE nos caminhos corretos e testar instalação/upgrade/remoção em ambientes descartáveis |

Pacotes deb/rpm poderiam inicialmente ser anexados à própria release, sem APT
ou YUM. Um repositório externo, assinatura de pacotes e sua distribuição exigem
outra decisão. Não há arquivos nFPM ou publicação Homebrew habilitados nesta etapa.
Referências: [Homebrew no GoReleaser](https://goreleaser.com/customization/publish/homebrew_casks/)
e [nFPM no GoReleaser](https://goreleaser.com/customization/nfpm/).
