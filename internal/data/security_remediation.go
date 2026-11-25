package data

import "strings"

type Remediation struct {
	Target  string
	YAML    []string
	Caution string
}

func remediationTarget(w WorkloadPosture) string {
	switch w.Resolution {
	case OwnerStandalone:
		return "Pod sem controlador: recrie o pod com o spec corrigido."
	case OwnerUnresolved:
		return "Owner não resolvido: alvo da correção desconhecido; não edite o pod controlado."
	case OwnerExternal:
		return "Controlador não reconhecido " + w.Key.Kind + ": corrija o template desse controlador."
	}
	switch w.Key.Kind {
	case "ReplicaSet":
		return "template de quem gerencia ReplicaSet/" + w.Name + "; controlador acima não observado nesta versão."
	case "Job":
		return "spec.template.spec de Job/" + w.Name + "; se pertencer a um CronJob, corrija spec.jobTemplate do CronJob."
	}
	return "spec.template.spec de " + w.Key.Kind + "/" + w.Name + "."
}

func containerList(field string) string {
	switch {
	case strings.HasPrefix(field, "spec.containers["):
		return "containers"
	case strings.HasPrefix(field, "spec.initContainers["):
		return "initContainers"
	case strings.HasPrefix(field, "spec.ephemeralContainers["):
		return "ephemeralContainers"
	}
	return ""
}

func containerYAML(list, name string, body ...string) []string {
	out := []string{list + ":", "- name: " + name}
	for _, l := range body {
		out = append(out, "  "+l)
	}
	return out
}

func RemediationFor(r WorkloadRule, w WorkloadPosture) Remediation {
	out := Remediation{Target: remediationTarget(w)}
	f := r.Evidence
	list := containerList(f.Field)
	container := func(caution string, body ...string) Remediation {
		switch {
		case list == "ephemeralContainers":
			out.Caution = "Containers efêmeros não são editáveis; remova o container de debug recriando o pod."
		case list == "" || f.Container == "":
			out.Caution = "Container da evidência não identificado; nenhuma sugestão YAML gerada."
		default:
			out.YAML, out.Caution = containerYAML(list, f.Container, body...), caution
		}
		return out
	}
	switch r.Rule {
	case "privileged":
		return container("A aplicação pode depender de acesso privilegiado; valide antes de aplicar.", "securityContext:", "  privileged: false")
	case "allowPrivilegeEscalation":
		return container("Valide se algum processo depende de setuid ou de ganho de privilégio.", "securityContext:", "  allowPrivilegeEscalation: false")
	case "runAsRoot", "runAsNonRoot":
		return container("A imagem precisa funcionar com um usuário sem privilégios.", "securityContext:", "  runAsNonRoot: true", "  runAsUser: 10001")
	case "seccomp":
		return container("Perfis Localhost revisados também são aceitos.", "securityContext:", "  seccompProfile:", "    type: RuntimeDefault")
	case "capabilitiesAdded":
		return container("Substitui capabilities por completo: add vazio remove o que foi concedido, inclusive ALL e SYS_ADMIN; reconstrua em add apenas a allowlist mínima necessária.", "securityContext:", "  capabilities:", "    add: []", "    drop: [\"ALL\"]")
	case "capabilitiesDrop":
		return container("Drop ALL não basta se capabilities.add conceder ALL ou SYS_ADMIN; mantenha em add apenas o necessário.", "securityContext:", "  capabilities:", "    drop: [\"ALL\"]")
	case "imageDigest", "imageLatest":
		return container("Use o digest verificado no registry; o valor acima é um marcador.", "image: <repositorio>@sha256:<digest>")
	case "limit-cpu":
		return container("Defina o valor conforme o uso medido.", "resources:", "  limits:", "    cpu: <valor>")
	case "limit-memory":
		return container("Defina o valor conforme o uso medido.", "resources:", "  limits:", "    memory: <valor>")
	case "limits":
		return container("Defina os valores conforme o uso medido.", "resources:", "  limits:", "    cpu: <valor>", "    memory: <valor>")
	case "hostNetwork", "hostPID", "hostIPC":
		out.YAML, out.Caution = []string{r.Rule + ": false"}, "Valide se a aplicação depende do namespace do host."
	case "serviceAccountToken":
		out.YAML, out.Caution = []string{"automountServiceAccountToken: false"}, "Mantenha montado se a aplicação usa a API do Kubernetes."
	case "hostPath":
		out.Caution = "Substitua o volume hostPath por PVC, emptyDir ou configMap conforme a necessidade."
	default:
		out.Caution = "Sem sugestão YAML para esta regra."
	}
	return out
}
