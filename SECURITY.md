# Política de segurança

## Reporte privado

Não publique vulnerabilidades, credenciais ou dados de clusters em issues,
pull requests, logs de CI ou discussões públicas.

Use [Report a vulnerability](https://github.com/vynazevedo/lazykube/security/advisories/new)
no GitHub Security Advisories para iniciar um relato privado aos mantenedores.
Inclua versão ou SHA, impacto, passos de reprodução com dados sintéticos e,
quando possível, um teste que não dependa de um cluster real.

O reporte privado precisa estar habilitado nas configurações do repositório.
Se o botão não estiver disponível, não publique os detalhes. Solicite a ativação
ao mantenedor em uma issue contendo somente esse pedido, sem descrever a falha.
A divulgação e a release de correção serão coordenadas no advisory privado.
Não há garantia de prazo de resposta para este projeto mantido voluntariamente.

## Versões e limites

Enquanto a 1.0 não for publicada, a triagem se concentra no código atual da main.
Após a publicação, a linha 1.0.x será a linha inicial de manutenção. Versões antigas
podem exigir atualização; não há promessa de backports para linhas anteriores.

A matriz alvo é Kubernetes 1.34 a 1.36. Leia os limites de validação no README:
um teste de API com envtest não demonstra segurança de todos os fluxos de um
cluster. O cliente também não substitui RBAC ou controles no servidor.

O instalador verifica SHA-256 antes de extrair. Releases produzidas pelo workflow
com assinatura keyless permitem verificar a identidade do workflow via Sigstore.
Um checksum sem verificação de assinatura detecta corrupção, mas não comprova
sozinho a identidade do publicador.
