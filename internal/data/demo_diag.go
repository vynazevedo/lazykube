package data

import (
	"fmt"
	"sort"
	"strings"
)

const demoTail = 500

var shipperLogs = []demoLine{
	{LevelInfo, "shipped batch size=128 sink=loki"},
	{LevelDebug, "buffer flush ok bytes=48213"},
	{LevelWarn, "sink slow, retrying in 500ms"},
	{LevelInfo, "shipped batch size=97 sink=loki"},
}

var initLogs = []demoLine{
	{LevelInfo, "waiting for configmap xcx/lingqu-config"},
	{LevelDebug, "configmap not found, retry in 2s"},
}

var previousLogs = map[string][]LogLine{
	"bilibili-live-7f6b8d9c5-mn4tz/app": {
		{Time: "19:58:09", Level: LevelInfo, Message: "ws connected room=21452505 shard=3"},
		{Time: "19:58:11", Level: LevelError, Message: "panic: runtime error: invalid memory address or nil pointer dereference"},
		{Time: "19:58:11", Level: LevelError, Message: "goroutine 41 [running]: main.(*Room).handle(0x0)"},
		{Time: "19:58:12", Level: LevelError, Message: "exit status 1"},
	},
	"odoo-7575bf6d8-9wjvk/app": {
		{Time: "08:12:40", Level: LevelInfo, Message: "HTTP service (werkzeug) running on 0.0.0.0:8069"},
		{Time: "08:13:02", Level: LevelWarn, Message: "worker 7 memory usage 121Mi above soft limit"},
		{Time: "08:13:05", Level: LevelError, Message: "MemoryError while rendering report sale.order"},
	},
	"svclb-traefik-8c2f1-6n2vb/lb-tcp-80": {
		{Time: "20:13:51", Level: LevelInfo, Message: "+ iptables -t nat -I PREROUTING -p tcp --dport 80"},
		{Time: "20:13:51", Level: LevelError, Message: "iptables v1.8.8: can't initialize iptables table `nat': Permission denied"},
	},
}

func running(name, image string, restarts int, since string) Container {
	return Container{Name: name, Image: image, Kind: ContainerApp, Phase: PhaseRunning, Ready: true, Started: true, Restarts: restarts, Since: since}
}

func completed(name, image string, kind ContainerKind, finished, ago string) Container {
	term := &Termination{Reason: "Completed", Finished: finished, Age: ago}
	return Container{Name: name, Image: image, Kind: kind, Phase: PhaseTerminated, Reason: "Completed", Current: term}
}

func demoContainers(name, image string) []Container {
	switch {
	case strings.HasPrefix(name, "bilibili-live"):
		app := running("app", image, 2, "17m")
		app.Last = &Termination{Reason: "Error", ExitCode: 1, Message: "panic: runtime error: invalid memory address", Finished: "2026-10-16 19:58:12", Age: "17m"}
		return []Container{app, running("log-shipper", "registry.local/fluent-bit:3.1", 0, "119d")}
	case strings.HasPrefix(name, "psql"):
		return []Container{completed("init-perms", "registry.local/busybox:1.36", ContainerInit, "2026-08-13 09:02:11", "64d"), running("app", image, 0, "64d")}
	case strings.HasPrefix(name, "odoo"):
		app := running("app", image, 1, "12d")
		app.Last = &Termination{Reason: "OOMKilled", ExitCode: 137, Finished: "2026-10-04 08:13:06", Age: "12d"}
		return []Container{app}
	case strings.HasPrefix(name, "qiandao"), strings.HasPrefix(name, "lingqu-1605486000"):
		return []Container{completed("app", image, ContainerApp, "2026-10-16 19:34:02", "41m")}
	case strings.HasPrefix(name, "lingqu-1605489600"):
		wait := Container{Name: "wait-config", Image: "registry.local/busybox:1.36", Kind: ContainerInit, Phase: PhaseRunning, Started: true, Since: "8s"}
		app := Container{Name: "app", Image: image, Kind: ContainerApp, Phase: PhaseWaiting, Reason: "PodInitializing"}
		return []Container{wait, app}
	case strings.HasPrefix(name, "metrics-server"):
		app := running("app", image, 0, "119d")
		app.Ready = false
		return []Container{app}
	case strings.HasPrefix(name, "local-path-provisioner"):
		return []Container{{
			Name: "app", Image: "rancher/local-path-provisioner:v0.0.99", Kind: ContainerApp, Phase: PhaseWaiting,
			Reason:  "ImagePullBackOff",
			Message: `Back-off pulling image "rancher/local-path-provisioner:v0.0.99"`,
		}}
	case strings.HasPrefix(name, "svclb-traefik"):
		lb := Container{
			Name: "lb-tcp-80", Image: "rancher/klipper-lb:v0.4.4", Kind: ContainerApp, Phase: PhaseWaiting, Restarts: 7,
			Reason:  "CrashLoopBackOff",
			Message: "back-off 5m0s restarting failed container=lb-tcp-80 pod=" + name,
			Last:    &Termination{Reason: "Error", ExitCode: 1, Finished: "2026-10-16 20:13:51", Age: "1m"},
		}
		return []Container{lb}
	default:
		return []Container{running("app", image, 0, "119d")}
	}
}

func demoSummary(status PodStatus, containers []Container) (string, int, int) {
	ready, total := 0, 0
	reason := status.String()
	for _, c := range containers {
		if c.Kind == ContainerInit {
			if c.Phase != PhaseTerminated {
				reason = "Init:0/1"
			}
			continue
		}
		total++
		if c.Ready {
			ready++
		}
		if c.Phase == PhaseWaiting && c.Reason != "" && c.Reason != "PodInitializing" {
			reason = c.Reason
		}
	}
	return reason, ready, total
}

func demoPodEvents(p Pod) []Event {
	obj := "Pod/" + p.Name
	ev := func(age, typ, reason, msg string, count int) Event {
		return Event{UID: "demo-" + p.Namespace + "-" + p.Name + "-" + strings.ToLower(reason), Age: age, Type: typ, Reason: reason, Object: obj, Namespace: p.Namespace, Count: count, Message: msg}
	}
	switch {
	case strings.HasPrefix(p.Name, "bilibili-live"):
		return []Event{
			ev("17m", "Warning", "BackOff", "Back-off restarting failed container app in pod "+p.Name, 3),
			ev("17m", "Normal", "Pulled", `Container image "`+p.Image+`" already present on machine`, 2),
			ev("17m", "Normal", "Started", "Started container app", 2),
		}
	case strings.HasPrefix(p.Name, "odoo"):
		return []Event{
			ev("12d", "Warning", "OOMKilling", "Memory cgroup out of memory: Killed process 4121 (python3)", 1),
			ev("12d", "Normal", "Started", "Started container app", 1),
		}
	case strings.HasPrefix(p.Name, "lingqu-1605489600"):
		return []Event{
			ev("8s", "Normal", "Scheduled", "Successfully assigned xcx/"+p.Name+" to "+demoNode, 1),
			ev("7s", "Normal", "Started", "Started container wait-config", 1),
		}
	case strings.HasPrefix(p.Name, "metrics-server"):
		return []Event{ev("2m", "Warning", "Unhealthy", "Readiness probe failed: HTTP probe failed with statuscode: 500", 41)}
	case strings.HasPrefix(p.Name, "local-path-provisioner"):
		return []Event{
			ev("3m", "Warning", "Failed", `Failed to pull image "rancher/local-path-provisioner:v0.0.99": not found`, 6),
			ev("3m", "Normal", "BackOff", `Back-off pulling image "rancher/local-path-provisioner:v0.0.99"`, 12),
		}
	case strings.HasPrefix(p.Name, "svclb-traefik"):
		return []Event{ev("1m", "Warning", "BackOff", "Back-off restarting failed container lb-tcp-80 in pod "+p.Name, 29)}
	case strings.HasPrefix(p.Name, "qiandao"), strings.HasPrefix(p.Name, "lingqu-1605486000"):
		return []Event{ev("41m", "Normal", "Completed", "Job completed", 1)}
	default:
		return nil
	}
}

var demoClusterEvents = []Event{
	{UID: "demo-node-nodeready", Age: "3d", Type: "Normal", Reason: "NodeReady", Object: "Node/" + demoNode, Count: 1, Message: "Node " + demoNode + " status is now: NodeReady"},
	{UID: "demo-node-freediskspacefailed", Age: "5m", Type: "Warning", Reason: "FreeDiskSpaceFailed", Object: "Node/" + demoNode, Count: 2, Message: "failed to garbage collect required amount of images"},
}

func demoConditions(p Pod) string {
	check := func(ok bool) string {
		if ok {
			return "✓"
		}
		return "✗"
	}
	initialized := !strings.HasPrefix(p.Reason, "Init:")
	ready := p.Containers > 0 && p.Ready == p.Containers
	return fmt.Sprintf("Initialized %s Ready %s ContainersReady %s PodScheduled ✓", check(initialized), check(ready), check(ready))
}

func WarningsFirst(events []Event) {
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Type == "Warning" && events[j].Type != "Warning"
	})
}

func (d *Demo) Containers(pod string) []Container {
	i := d.index(pod)
	if i < 0 {
		return nil
	}
	return append([]Container(nil), d.pods[i].containers...)
}

func (d *Demo) PodLogs(req LogRequest, tick, n int) LogView {
	i := d.index(req.Pod)
	if i < 0 {
		return LogView{State: FeedUnavailable, Reason: "pod não encontrado"}
	}
	p := d.pods[i]
	c, ok := pickContainer(p.containers, req.Container)
	if !ok {
		return LogView{State: FeedUnavailable, Reason: "pod sem containers"}
	}
	view := LogView{Container: c.Name, Previous: req.Previous}
	if req.Previous {
		if !c.HasPrevious() {
			view.State, view.Reason = FeedUnavailable, fmt.Sprintf("sem instância anterior do container %s", c.Name)
			return view
		}
		lines := previousLogs[p.pod.Name+"/"+c.Name]
		if len(lines) == 0 {
			lines = []LogLine{{Time: "--:--:--", Level: LevelInfo, Message: "instância anterior sem saída registrada"}}
		}
		view.Lines = tailLines(lines, n)
		return view
	}
	switch {
	case c.Phase == PhaseWaiting:
		view.State = FeedUnavailable
		view.Reason = fmt.Sprintf("container %s aguardando: %s", c.Name, reasonOr(c.Reason, "sem status"))
	case c.Kind == ContainerInit && c.Phase == PhaseRunning:
		view.Lines = rotatingLogs(initLogs, tick, min(n, demoTail))
	case c.Name == "log-shipper":
		view.Lines = rotatingLogs(shipperLogs, tick, min(n, demoTail))
	case c.Phase == PhaseTerminated:
		view.Lines = tailLines([]LogLine{
			{Time: "19:34:01", Level: LevelInfo, Message: "job started"},
			{Time: "19:34:02", Level: LevelInfo, Message: "job finished ok"},
		}, n)
	default:
		view.Lines = rotatingLogs(demoLogs, tick, min(n, demoTail))
	}
	return view
}

func reasonOr(reason, fallback string) string {
	if reason == "" {
		return fallback
	}
	return reason
}

func tailLines(lines []LogLine, n int) []LogLine {
	if n <= 0 {
		return nil
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return append([]LogLine(nil), lines...)
}

func pickContainer(cs []Container, name string) (Container, bool) {
	for _, c := range cs {
		if c.Name == name {
			return c, true
		}
	}
	for _, c := range cs {
		if c.Kind == ContainerApp {
			return c, true
		}
	}
	if len(cs) > 0 {
		return cs[0], true
	}
	return Container{}, false
}

func (d *Demo) ScopedEvents(q EventQuery) EventFeed {
	feed := EventFeed{Scope: q.Scope}
	switch q.Scope {
	case ScopePod:
		feed.Target = q.Pod
		if i := d.index(q.Pod); i >= 0 {
			feed.Items = append(feed.Items, d.pods[i].events...)
		}
	case ScopeNamespace:
		feed.Target = q.Namespace
		for _, p := range d.pods {
			if p.pod.Namespace == q.Namespace {
				feed.Items = append(feed.Items, p.events...)
			}
		}
	case ScopeCluster:
		for _, p := range d.pods {
			feed.Items = append(feed.Items, p.events...)
		}
		feed.Items = append(feed.Items, demoClusterEvents...)
	}
	WarningsFirst(feed.Items)
	return feed
}

var _ Diagnostics = (*Demo)(nil)
