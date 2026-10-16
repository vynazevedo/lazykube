package kube

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"

	"github.com/vynazevedo/lazykube/internal/data"
)

const (
	maxDiagMessage   = 300
	defaultContainer = "kubectl.kubernetes.io/default-container"
)

type podSummary struct {
	reason   string
	status   data.PodStatus
	ready    int
	total    int
	restarts int
}

func restartable(c *corev1.Container) bool {
	return c != nil && c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways
}

func initSpecs(p *corev1.Pod) map[string]*corev1.Container {
	out := make(map[string]*corev1.Container, len(p.Spec.InitContainers))
	for i := range p.Spec.InitContainers {
		out[p.Spec.InitContainers[i].Name] = &p.Spec.InitContainers[i]
	}
	return out
}

func exitText(prefix string, t *corev1.ContainerStateTerminated) string {
	if t.Reason != "" {
		return prefix + t.Reason
	}
	if t.Signal != 0 {
		return fmt.Sprintf("%sSignal:%d", prefix, t.Signal)
	}
	return fmt.Sprintf("%sExitCode:%d", prefix, t.ExitCode)
}

func initReason(p *corev1.Pod, specs map[string]*corev1.Container, sum *podSummary) (string, bool) {
	for i, cs := range p.Status.InitContainerStatuses {
		sum.restarts += int(cs.RestartCount)
		spec := specs[cs.Name]
		switch {
		case cs.State.Terminated != nil && cs.State.Terminated.ExitCode == 0:
			continue
		case restartable(spec) && cs.Started != nil && *cs.Started:
			if cs.Ready {
				sum.ready++
			}
			continue
		case cs.State.Terminated != nil:
			return exitText("Init:", cs.State.Terminated), true
		case cs.State.Waiting != nil && cs.State.Waiting.Reason != "" && cs.State.Waiting.Reason != "PodInitializing":
			return "Init:" + cs.State.Waiting.Reason, true
		default:
			return fmt.Sprintf("Init:%d/%d", i, len(p.Spec.InitContainers)), true
		}
	}
	return "", false
}

func podInitialized(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodInitialized {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func podReadyCondition(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func summarize(p *corev1.Pod) podSummary {
	specs := initSpecs(p)
	sum := podSummary{total: len(p.Spec.Containers)}
	for _, c := range p.Spec.InitContainers {
		if restartable(&c) {
			sum.total++
		}
	}
	reason := string(p.Status.Phase)
	if p.Status.Reason != "" {
		reason = p.Status.Reason
	}
	if r, initializing := initReason(p, specs, &sum); initializing && !podInitialized(p) {
		reason = r
	} else {
		sum.restarts = 0
		for _, cs := range p.Status.InitContainerStatuses {
			if restartable(specs[cs.Name]) {
				sum.restarts += int(cs.RestartCount)
			}
		}
		running := false
		for i := len(p.Status.ContainerStatuses) - 1; i >= 0; i-- {
			cs := p.Status.ContainerStatuses[i]
			sum.restarts += int(cs.RestartCount)
			switch {
			case cs.State.Waiting != nil && cs.State.Waiting.Reason != "":
				reason = cs.State.Waiting.Reason
			case cs.State.Terminated != nil:
				reason = exitText("", cs.State.Terminated)
			case cs.Ready && cs.State.Running != nil:
				running = true
				sum.ready++
			}
		}
		if reason == "Completed" && running {
			reason = "NotReady"
			if podReadyCondition(p) {
				reason = "Running"
			}
		}
	}
	terminal := p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed
	switch {
	case p.DeletionTimestamp != nil && p.Status.Reason == "NodeLost":
		reason = "Unknown"
	case p.DeletionTimestamp != nil && !terminal:
		reason = "Terminating"
	}
	if reason == "" {
		reason = "Unknown"
	}
	sum.reason = data.Sanitize(reason)
	sum.status = statusFor(p.Status.Phase, sum.reason)
	return sum
}

func initProgress(reason string) bool {
	rest, ok := strings.CutPrefix(reason, "Init:")
	if !ok {
		return false
	}
	done, total, ok := strings.Cut(rest, "/")
	return ok && digits(done) && digits(total)
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func statusFor(phase corev1.PodPhase, reason string) data.PodStatus {
	failed := failedReasons[reason] || strings.HasPrefix(reason, "ExitCode:") || strings.HasPrefix(reason, "Signal:")
	switch {
	case reason == "Running" || reason == "NotReady":
		return data.StatusRunning
	case reason == "Completed" || reason == "Succeeded":
		return data.StatusCompleted
	case reason == "Terminating" || reason == "Unknown":
		return data.StatusUnknown
	case reason == "ContainerCreating" || reason == "PodInitializing":
		return data.StatusContainerCreating
	case initProgress(reason):
		return data.StatusPending
	case strings.HasPrefix(reason, "Init:"), failed:
		return data.StatusFailed
	}
	switch phase {
	case corev1.PodRunning:
		return data.StatusRunning
	case corev1.PodPending:
		return data.StatusPending
	case corev1.PodSucceeded:
		return data.StatusCompleted
	case corev1.PodFailed:
		return data.StatusFailed
	default:
		return data.StatusUnknown
	}
}

func clip(s string) string {
	s = strings.Join(strings.Fields(data.Sanitize(s)), " ")
	if len(s) <= maxDiagMessage {
		return s
	}
	cut := maxDiagMessage
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func toTermination(t *corev1.ContainerStateTerminated, now time.Time, loc *time.Location) *data.Termination {
	if t == nil {
		return nil
	}
	out := &data.Termination{
		Reason:   data.Sanitize(t.Reason),
		Message:  clip(t.Message),
		ExitCode: int(t.ExitCode),
		Signal:   int(t.Signal),
	}
	if !t.FinishedAt.IsZero() {
		out.Finished = t.FinishedAt.In(loc).Format("2006-01-02 15:04:05")
		out.Age = age(t.FinishedAt, now)
	}
	return out
}

func toContainer(name, image string, kind data.ContainerKind, cs *corev1.ContainerStatus, now time.Time, loc *time.Location) data.Container {
	c := data.Container{Name: data.Sanitize(name), Image: data.Sanitize(image), Kind: kind}
	if cs == nil {
		c.Reason = "sem status reportado"
		return c
	}
	c.Ready, c.Restarts = cs.Ready, int(cs.RestartCount)
	c.Started = cs.Started != nil && *cs.Started
	switch {
	case cs.State.Running != nil:
		c.Phase = data.PhaseRunning
		c.Since = age(cs.State.Running.StartedAt, now)
	case cs.State.Terminated != nil:
		c.Phase = data.PhaseTerminated
		c.Current = toTermination(cs.State.Terminated, now, loc)
		c.Reason = exitText("", cs.State.Terminated)
	case cs.State.Waiting != nil:
		c.Reason = data.Sanitize(cs.State.Waiting.Reason)
		c.Message = clip(cs.State.Waiting.Message)
	}
	c.Last = toTermination(cs.LastTerminationState.Terminated, now, loc)
	return c
}

func statusIndex(list []corev1.ContainerStatus) map[string]*corev1.ContainerStatus {
	out := make(map[string]*corev1.ContainerStatus, len(list))
	for i := range list {
		out[list[i].Name] = &list[i]
	}
	return out
}

func toContainers(p *corev1.Pod, now time.Time, loc *time.Location) []data.Container {
	out := make([]data.Container, 0, len(p.Spec.InitContainers)+len(p.Spec.Containers)+len(p.Spec.EphemeralContainers))
	inits := statusIndex(p.Status.InitContainerStatuses)
	for i := range p.Spec.InitContainers {
		c := &p.Spec.InitContainers[i]
		kind := data.ContainerInit
		if restartable(c) {
			kind = data.ContainerSidecar
		}
		out = append(out, toContainer(c.Name, c.Image, kind, inits[c.Name], now, loc))
	}
	apps := statusIndex(p.Status.ContainerStatuses)
	for _, c := range p.Spec.Containers {
		out = append(out, toContainer(c.Name, c.Image, data.ContainerApp, apps[c.Name], now, loc))
	}
	ephemeral := statusIndex(p.Status.EphemeralContainerStatuses)
	for _, c := range p.Spec.EphemeralContainers {
		out = append(out, toContainer(c.Name, c.Image, data.ContainerEphemeral, ephemeral[c.Name], now, loc))
	}
	return out
}

func defaultContainerName(p *corev1.Pod) string {
	if name := p.Annotations[defaultContainer]; name != "" && containerSpec(p, name) {
		return name
	}
	if len(p.Spec.Containers) > 0 {
		return p.Spec.Containers[0].Name
	}
	return ""
}

func containerSpec(p *corev1.Pod, name string) bool {
	for _, c := range p.Spec.InitContainers {
		if c.Name == name {
			return true
		}
	}
	for _, c := range p.Spec.Containers {
		if c.Name == name {
			return true
		}
	}
	for _, c := range p.Spec.EphemeralContainers {
		if c.Name == name {
			return true
		}
	}
	return false
}

func lastTermination(p *corev1.Pod, name string) *corev1.ContainerStateTerminated {
	for _, list := range [][]corev1.ContainerStatus{p.Status.InitContainerStatuses, p.Status.ContainerStatuses, p.Status.EphemeralContainerStatuses} {
		for _, cs := range list {
			if cs.Name == name {
				return cs.LastTerminationState.Terminated
			}
		}
	}
	return nil
}

func (s *Source) Containers(key string) []data.Container {
	p := s.pod(key)
	if p == nil {
		return nil
	}
	return toContainers(p, s.opts.Now(), s.opts.Location)
}
