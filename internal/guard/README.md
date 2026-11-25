# Guardrails de acoes

O processo inicia somente leitura. Apenas `--read-write` habilita a tentativa de
acoes nesta execucao. Nao existe persistencia nem tecla para alterar a politica.
`Environment.ReadOnly`, `Environment.Protected` e configuracao invalida prevalecem
sobre a flag. Somente nomes exatos configurados em `--protected-contexts` sao
protegidos; nao ha classificacao por substring. O resolvedor de ambientes e o ponto
de extensao para selecao de contexto.

`Session` fixa contexto, namespace, identidade local da sessao e geracao.
`Executor` recebe clientes vinculados a essa sessao. Ao trocar contexto ou
identidade, o consumidor fecha a fonte anterior, cria outra e descarta previews.
`Close` cancela operacoes ativas; nao ha redirecionamento para outro cliente.
A identidade registrada e local, nao uma afirmacao sobre o usuario autenticado
pelo servidor. Nenhuma credencial participa dela.

`Prepare` valida politica, SSAR exata e UID/resourceVersion, registra a tentativa
e executa dry-run server-side. O preview tem prazo de dois minutos e um ID de uso
unico. `Execute` exige confirmacao, repete SSAR e valida a identidade do recurso,
sincroniza a intencao no audit e faz uma unica requisicao, sem repeticao do
cliente. Leituras, SSAR e escritas do executor fazem uma tentativa cada; a falha
vira recusa e so um gesto novo repete. Informers e listas mantem os proprios
retries. Todo redirect HTTP e recusado no cliente de acoes e
na SSAR: um 3xx em dry-run reprova o preview, sem segundo request. Delete, edit, cordon, uncordon e scale para zero
exigem o nome do recurso. Nao existe create. Edit usa PUT de um objeto
existente com UID e resourceVersion. `SupportsAction` e a allowlist compartilhada
pelo executor e pelos futuros handlers: todos os GVRs abaixo usam versao v1.

| Grupo | Recurso | Acoes elegiveis |
| --- | --- | --- |
| core | pods | delete, edit, exec e port-forward reservados |
| core | configmaps, services | edit |
| apps | deployments, statefulsets | delete, edit, scale, restart |
| apps | daemonsets | delete, edit, restart |
| batch | jobs, cronjobs | delete, edit |
| core | nodes | cordon, uncordon |

Grupos, versoes e recursos fora dessa lista sao negados antes de SSAR ou dry-run.
Nao ha acao generica para Secrets, RBAC, admission, PV ou CRs. Prepare audita
negativas com ID e desfecho seguro, sem intent mutante. O preflight de Execute
recebe um contexto limitado ao prazo restante e verifica novamente a expiracao
depois da autorizacao, da validacao e do intent duravel, imediatamente antes do
envio. Vencimento antes do envio e cancelamento, nunca sucesso ou tentativa nova.

| Acao | SSAR de leitura | SSAR de escrita | Dry-run |
| --- | --- | --- | --- |
| Delete | get no recurso | delete no recurso | All |
| Scale | get em recurso/scale | update em recurso/scale | All |
| Restart | get no recurso | patch no recurso | All |
| Edit existente | get no recurso | update no recurso | All |
| Cordon e uncordon | get nodes | patch nodes | All |
| Exec reservado | get pods | create pods/exec para POST | sem suporte ativo |
| Port-forward reservado | get pods | create pods/portforward para POST | sem suporte ativo |

Todas incluem grupo, versao, namespace e nome exatos. O transporte ativo ainda
nao esta habilitado. Sua implementacao deve escolher e testar um unico protocolo;
WebSocket com GET exige alterar a SSAR para get no subrecurso. Nao ha fallback.
Port-forward aceita somente loopback e deve terminar com a sessao. Shell local,
create, Helm write e nodes/proxy nao fazem parte deste executor. A coleta de rede
via proxy fica desativada nas fontes reais.

O audit JSONL contem somente campos definidos em `AuditEvent`. Nao recebe payload,
comando, resposta do servidor ou credencial. O diretorio e 0700 e cada arquivo e
0600, com verificacao de proprietario, links e tipo. Sao dez arquivos no total,
com limite de 10 MiB por arquivo e descarte apos 30 dias. A limpeza ocorre antes
de gravar cada evento; um processo inativo nao faz limpeza em segundo plano.
Um segmento so e removido quando todos os seus registros e a data de modificacao
ultrapassam 30 dias. A verificacao de arquivos antigos usa leitura limitada por
linha e por segmento. Se dez segmentos estiverem ocupados e a rotacao exigir
sobrescrever registros ainda retidos, o audit falha e bloqueia novas acoes.
Nao se apagam registros recentes para liberar espaco. Qualquer falha bloqueia
novas acoes neste executor. Falha apos
o envio preserva resultado desconhecido quando nao ha confirmacao do servidor;
nao ha repeticao automatica.

Linux foi exercitado com fakes e servidor HTTP local. Darwin e Windows permanecem
preview ate validacao nativa. No Windows, verificacao de ACL privada ainda nao foi
validada: o audit falha fechado e bloqueia acoes. Nao ha afirmacao de suporte de
mutacoes nessa plataforma. A matriz alvo Kubernetes e 1.34, 1.35 e 1.36; fakes e
HTTP sintetico validam contratos, nao substituem evidencia por versao de servidor.

Nodes sao o unico alvo de escopo cluster: o namespace do alvo e vazio. O PATCH
JSON testa UID e resourceVersion e altera somente `spec.unschedulable`, com os
mesmos bytes no dry-run e no envio. Estado ja igual, em scale ou node, e recusado
com `no_change` antes do dry-run. Conflito dos testes do PATCH e alvo obsoleto.

Todo plano valido consumido deixa um unico desfecho no audit: recusa de politica ou
de confirmacao, cancelamento, expiracao, remocao de plano vencido ou resultado. Um
ID inexistente nao gera evento, porque nao ha alvo conhecido. Se o registro da
recusa falhar, o plano continua consumido e novas acoes ficam bloqueadas.

`NotSent` e a unica forma de um backend afirmar que falhou antes da requisicao
mutante, como a leitura interna do restart. Qualquer outro erro de envio continua
`outcome_unknown`, inclusive cancelamento e timeout.

`Observe` faz uma leitura pontual do alvo, sem mutex, e so depois da politica e da
allowlist: em somente leitura nenhuma requisicao sai. `Discard` consome um preview
sem enviar nada e registra o cancelamento. `AuditBlocked` informa a falha de audit
para a tela. `Close` cancela as operacoes e espera ate dois segundos pelo registro
do resultado em andamento.

A tecla `o` da TUI usa apenas scale, restart, delete, cordon e uncordon. Edit,
exec e port-forward continuam sem handler. O header mostra o modo, a protecao, a
configuracao invalida e o bloqueio por audit independentemente de cores. A demo
continua sintetica e somente leitura.
