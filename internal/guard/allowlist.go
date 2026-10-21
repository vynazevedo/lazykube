package guard

func SupportsAction(action Action, target Target) bool {
	if target.Version != "v1" {
		return false
	}
	switch target.Group {
	case "":
		switch target.Resource {
		case "pods":
			return action == Delete || action == Edit || action == Exec || action == PortForward
		case "configmaps", "services":
			return action == Edit
		}
	case "apps":
		switch target.Resource {
		case "deployments", "statefulsets":
			return action == Delete || action == Edit || action == Scale || action == Restart
		case "daemonsets":
			return action == Delete || action == Edit || action == Restart
		}
	case "batch":
		switch target.Resource {
		case "jobs", "cronjobs":
			return action == Delete || action == Edit
		}
	}
	return false
}
