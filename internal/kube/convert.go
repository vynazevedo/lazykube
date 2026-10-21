package kube

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/duration"
	"sigs.k8s.io/yaml"

	"github.com/vynazevedo/lazykube/internal/data"
)

var failedReasons = map[string]bool{
	"CrashLoopBackOff":           true,
	"ImagePullBackOff":           true,
	"ErrImagePull":               true,
	"InvalidImageName":           true,
	"CreateContainerConfigError": true,
	"CreateContainerError":       true,
	"RunContainerError":          true,
	"Error":                      true,
	"OOMKilled":                  true,
}

var levelPattern = regexp.MustCompile(`(?i)\b(FATAL|ERROR|ERR|WARNING|WARN|DEBUG|INFO)\b`)

func age(t metav1.Time, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return duration.HumanDuration(now.Sub(t.Time))
}

func milli(q resource.Quantity) int {
	return int(q.MilliValue())
}

func mebi(q resource.Quantity) int {
	return int(q.Value() >> 20)
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	units := []string{"Ki", "Mi", "Gi", "Ti", "Pi"}
	v := float64(b) / unit
	i := 0
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f%s", v, units[i])
}

func podKey(namespace, name string) string {
	return data.PodKey(namespace, name)
}

func podStatus(p *corev1.Pod) data.PodStatus {
	return summarize(p).status
}

func resources(p *corev1.Pod) (cpuReq, cpuLim, memReq, memLim int) {
	for _, c := range p.Spec.Containers {
		cpuReq += milli(c.Resources.Requests[corev1.ResourceCPU])
		cpuLim += milli(c.Resources.Limits[corev1.ResourceCPU])
		memReq += mebi(c.Resources.Requests[corev1.ResourceMemory])
		memLim += mebi(c.Resources.Limits[corev1.ResourceMemory])
	}
	return
}

func controller(p *corev1.Pod) string {
	for _, o := range p.OwnerReferences {
		if o.Controller != nil && *o.Controller {
			return o.Kind + "/" + o.Name
		}
	}
	if len(p.OwnerReferences) > 0 {
		o := p.OwnerReferences[0]
		return o.Kind + "/" + o.Name
	}
	return "-"
}

func toPod(p *corev1.Pod, now time.Time) data.Pod {
	cpuReq, cpuLim, memReq, memLim := resources(p)
	image := ""
	if len(p.Spec.Containers) > 0 {
		image = p.Spec.Containers[0].Image
	}
	sum := summarize(p)
	return data.Pod{
		Name:       p.Name,
		Namespace:  p.Namespace,
		Status:     sum.status,
		Reason:     sum.reason,
		Ready:      sum.ready,
		Containers: sum.total,
		Restarts:   sum.restarts,
		Age:        age(p.CreationTimestamp, now),
		IP:         p.Status.PodIP,
		Node:       p.Spec.NodeName,
		CPURequest: cpuReq,
		CPULimit:   cpuLim,
		MemRequest: memReq,
		MemLimit:   memLim,
		Image:      image,
		Controller: controller(p),
		QoS:        string(p.Status.QOSClass),
	}
}

func nodeAddress(n *corev1.Node) string {
	for _, a := range n.Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			return a.Address
		}
	}
	if len(n.Status.Addresses) > 0 {
		return n.Status.Addresses[0].Address
	}
	return "-"
}

func nodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func toNode(n *corev1.Node, pods int, memUsed int64, memKnown bool) data.Node {
	alloc := n.Status.Allocatable
	if len(alloc) == 0 {
		alloc = n.Status.Capacity
	}
	memTotal := alloc.Memory().Value()
	out := data.Node{
		Name:        n.Name,
		Address:     nodeAddress(n),
		Ready:       nodeReady(n),
		MemUsed:     "n/a",
		MemTotal:    formatBytes(memTotal),
		Pods:        pods,
		PodCapacity: int(alloc.Pods().Value()),
		Arch:        n.Status.NodeInfo.Architecture,
	}
	if memKnown {
		out.MemUsed = formatBytes(memUsed)
		if memTotal > 0 {
			out.MemFraction = min(1, float64(memUsed)/float64(memTotal))
		}
	}
	return out
}

func toDeployment(d *appsv1.Deployment) data.Deployment {
	desired := 1
	if d.Spec.Replicas != nil {
		desired = int(*d.Spec.Replicas)
	}
	return data.Deployment{
		Namespace: d.Namespace,
		Name:      d.Name,
		Ready:     int(d.Status.ReadyReplicas),
		Desired:   desired,
	}
}

func eventTime(e *corev1.Event) time.Time {
	switch {
	case !e.LastTimestamp.IsZero():
		return e.LastTimestamp.Time
	case !e.EventTime.IsZero():
		return e.EventTime.Time
	case !e.FirstTimestamp.IsZero():
		return e.FirstTimestamp.Time
	default:
		return e.CreationTimestamp.Time
	}
}

func eventCount(e *corev1.Event) int {
	if e.Series != nil && e.Series.Count > 0 {
		return int(e.Series.Count)
	}
	return max(1, int(e.Count))
}

func toEvents(events []*corev1.Event, now time.Time, limit int) []data.Event {
	sorted := append([]*corev1.Event(nil), events...)
	sort.SliceStable(sorted, func(i, j int) bool {
		wi, wj := sorted[i].Type == corev1.EventTypeWarning, sorted[j].Type == corev1.EventTypeWarning
		if wi != wj {
			return wi
		}
		return eventTime(sorted[i]).After(eventTime(sorted[j]))
	})
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	out := make([]data.Event, len(sorted))
	for i, e := range sorted {
		message, truncated := clipTo(e.Message, data.EventPreviewBytes)
		out[i] = data.Event{
			UID:       string(e.UID),
			Age:       age(metav1.NewTime(eventTime(e)), now),
			Type:      sanitize(e.Type),
			Reason:    sanitize(e.Reason),
			Object:    sanitize(e.InvolvedObject.Kind + "/" + e.InvolvedObject.Name),
			Namespace: sanitize(e.Namespace),
			Count:     eventCount(e),
			Message:   message,
			Truncated: truncated,
		}
	}
	return out
}

func quantityText(cpu, mem int) string {
	if cpu == 0 && mem == 0 {
		return "-"
	}
	return fmt.Sprintf("cpu: %dm, memory: %dMi", cpu, mem)
}

func conditions(p *corev1.Pod) string {
	parts := make([]string, 0, len(p.Status.Conditions))
	for _, c := range p.Status.Conditions {
		mark := "✓"
		if c.Status != corev1.ConditionTrue {
			mark = "✗"
		}
		parts = append(parts, string(c.Type)+" "+mark)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func tolerations(p *corev1.Pod) string {
	parts := make([]string, 0, len(p.Spec.Tolerations))
	for _, t := range p.Spec.Tolerations {
		s := t.Key
		if t.Effect != "" {
			s += ":" + string(t.Effect)
		}
		if t.TolerationSeconds != nil {
			s += fmt.Sprintf(" for %ds", *t.TolerationSeconds)
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func describePod(p *corev1.Pod, nodeAddr string) []data.Field {
	cpuReq, cpuLim, memReq, memLim := resources(p)
	sum := summarize(p)
	node := p.Spec.NodeName
	if node == "" {
		node = "-"
	} else if nodeAddr != "" {
		node += "/" + nodeAddr
	}
	images := make([]string, 0, len(p.Spec.Containers))
	for _, c := range p.Spec.Containers {
		images = append(images, c.Image)
	}
	fields := []data.Field{
		{Key: "Name", Value: p.Name},
		{Key: "Namespace", Value: p.Namespace},
		{Key: "Node", Value: node},
		{Key: "Status", Value: sum.reason},
		{Key: "Ready", Value: fmt.Sprintf("%d/%d", sum.ready, sum.total)},
		{Key: "IP", Value: p.Status.PodIP},
		{Key: "Controlled By", Value: controller(p)},
		{Key: "Image", Value: strings.Join(images, ", ")},
		{Key: "Restart Count", Value: fmt.Sprint(sum.restarts)},
		{Key: "Requests", Value: quantityText(cpuReq, memReq)},
		{Key: "Limits", Value: quantityText(cpuLim, memLim)},
		{Key: "QoS Class", Value: string(p.Status.QOSClass)},
		{Key: "Conditions", Value: conditions(p)},
		{Key: "Tolerations", Value: tolerations(p)},
	}
	for i := range fields {
		fields[i].Value = sanitize(fields[i].Value)
	}
	return fields
}

func manifest(p *corev1.Pod) []string {
	c := p.DeepCopy()
	c.ManagedFields = nil
	c.APIVersion, c.Kind = "v1", "Pod"
	raw, err := yaml.Marshal(c)
	if err != nil {
		return []string{"# " + sanitize(err.Error())}
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	for i, l := range lines {
		lines[i] = sanitize(l)
	}
	return lines
}

func detectLevel(msg string) data.Level {
	m := levelPattern.FindString(msg)
	switch strings.ToUpper(m) {
	case "FATAL", "ERROR", "ERR":
		return data.LevelError
	case "WARNING", "WARN":
		return data.LevelWarn
	case "DEBUG":
		return data.LevelDebug
	default:
		return data.LevelInfo
	}
}

func parseLogLine(raw string, loc *time.Location) (data.LogLine, time.Time) {
	ts, msg, ok := strings.Cut(raw, " ")
	if !ok {
		ts, msg = raw, ""
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		msg = sanitize(raw)
		return data.LogLine{Time: "--:--:--", Level: detectLevel(msg), Message: msg}, time.Time{}
	}
	msg = sanitize(msg)
	return data.LogLine{Time: t.In(loc).Format("15:04:05"), Level: detectLevel(msg), Message: msg}, t
}

func sanitize(s string) string {
	return data.Sanitize(s)
}
