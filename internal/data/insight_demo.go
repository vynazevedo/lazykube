package data

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vynazevedo/lazykube/internal/insight"
)

const demoRestartEvery = 600

var _ insight.ObservationSource = (*Demo)(nil)

type demoObservations struct {
	mu        sync.Mutex
	clock     insight.Clock
	inventory *insight.Inventory
	base      time.Time
	step      int
}

func (d *Demo) ObserveWith(clock insight.Clock) {
	if clock == nil {
		return
	}
	d.obs = &demoObservations{clock: clock, inventory: insight.NewInventory(clock), base: clock.Now(), step: -1}
	d.publishObservations(0)
}

func (d *Demo) Observations(after insight.Cursor) insight.ObservationBatch {
	if d.obs == nil {
		return insight.ObservationBatch{Cursor: after, Synthetic: true, Coverage: insight.SourceCoverage{Inventory: insight.Coverage{State: insight.CoverageUnavailable, Reason: "demo sem relogio de observacao"}}}
	}
	d.obs.mu.Lock()
	defer d.obs.mu.Unlock()
	return d.obs.inventory.Observations(after)
}

func (d *Demo) AdvanceObservations(tick int) bool {
	if d.obs == nil {
		return false
	}
	return d.publishObservations(max(0, tick) / demoRestartEvery)
}

func (d *Demo) publishObservations(step int) bool {
	d.obs.mu.Lock()
	defer d.obs.mu.Unlock()
	if step == d.obs.step {
		return false
	}
	d.obs.step = step
	source := insight.SourceIdentity{SessionID: "demo", Context: d.context, Generation: 1}
	pods := make([]insight.PodObservation, 0, len(d.pods))
	for _, p := range d.pods {
		pods = append(pods, demoObservation(p.pod, step, d.obs.base))
	}
	return d.obs.inventory.Publish(source, d.namespace, true, pods, insight.Coverage{State: insight.CoverageComplete})
}

func demoObservation(p Pod, step int, base time.Time) insight.PodObservation {
	ready := insight.Condition{Kind: insight.ConditionReady, Name: "Ready", Status: insight.TruthTrue}
	obs := insight.PodObservation{
		Entity: insight.EntityRef{
			EntityKey:       insight.EntityKey{Version: "v1", Kind: "Pod", Namespace: p.Namespace, Name: p.Name, UID: p.UID},
			ResourceVersion: "1",
		},
		CreatedAt: base.Add(-119 * 24 * time.Hour),
		StartedAt: base.Add(-119 * 24 * time.Hour),
		Phase:     insight.PodRunning,
		NodeName:  demoNode,
	}
	running := func(name, image string, restarts int32) insight.ContainerObservation {
		return insight.ContainerObservation{Name: name, Image: image, StatusObserved: true, Ready: insight.TruthTrue, Started: insight.TruthTrue, RestartCount: restarts, Phase: insight.ContainerRunning, StartedAt: base.Add(-time.Hour)}
	}
	switch {
	case strings.HasPrefix(p.Name, "svclb-traefik"):
		rv := strconv.Itoa(step + 1)
		obs.Entity.ResourceVersion = rv
		ready.Status = insight.TruthFalse
		lb := insight.ContainerObservation{
			Name: "lb-tcp-80", Image: "rancher/klipper-lb:v0.4.4", StatusObserved: true, Ready: insight.TruthFalse, Started: insight.TruthFalse,
			RestartCount: int32(7 + step), Phase: insight.ContainerWaiting, Reason: insight.ReasonCrashLoopBackOff, ReasonText: "CrashLoopBackOff",
			LastTermination: &insight.Termination{Reason: insight.ReasonOther, ReasonText: "Error", ExitCode: 1, FinishedAt: base.Add(-time.Minute)},
		}
		obs.Containers = []insight.ContainerObservation{lb}
	case strings.HasPrefix(p.Name, "local-path-provisioner"):
		obs.Phase = insight.PodPending
		ready.Status = insight.TruthFalse
		obs.Containers = []insight.ContainerObservation{{
			Name: "app", Image: "rancher/local-path-provisioner:v0.0.99", StatusObserved: true, Ready: insight.TruthFalse, Started: insight.TruthFalse,
			Phase: insight.ContainerWaiting, Reason: insight.ReasonImagePullBackOff, ReasonText: "ImagePullBackOff",
		}}
	case strings.HasPrefix(p.Name, "odoo"):
		app := running("app", p.Image, 1)
		app.LastTermination = &insight.Termination{Reason: insight.ReasonOOMKilled, ReasonText: "OOMKilled", ExitCode: 137, StartedAt: base.Add(-13 * 24 * time.Hour), FinishedAt: base.Add(-12 * 24 * time.Hour)}
		obs.Containers = []insight.ContainerObservation{app}
	case strings.HasPrefix(p.Name, "bilibili-live"):
		app := running("app", p.Image, 2)
		app.LastTermination = &insight.Termination{Reason: insight.ReasonOther, ReasonText: "Error", ExitCode: 1, FinishedAt: base.Add(-17 * time.Minute)}
		obs.Containers = []insight.ContainerObservation{app, running("log-shipper", "registry.local/fluent-bit:3.1", 0)}
	case strings.HasPrefix(p.Name, "lingqu-1605489600"):
		obs.Phase = insight.PodPending
		ready.Status = insight.TruthFalse
		wait := running("wait-config", "registry.local/busybox:1.36", 0)
		wait.Kind, wait.Ready = insight.ContainerInit, insight.TruthFalse
		app := insight.ContainerObservation{Name: "app", Image: p.Image, StatusObserved: true, Ready: insight.TruthFalse, Started: insight.TruthFalse, Phase: insight.ContainerWaiting, Reason: insight.ReasonPodInitializing, ReasonText: "PodInitializing"}
		obs.Containers = []insight.ContainerObservation{wait, app}
	case p.Status == StatusCompleted:
		obs.Phase = insight.PodSucceeded
		ready.Status = insight.TruthFalse
		obs.Containers = []insight.ContainerObservation{{
			Name: "app", Image: p.Image, StatusObserved: true, Ready: insight.TruthFalse, Started: insight.TruthFalse, Phase: insight.ContainerTerminated,
			CurrentTermination: &insight.Termination{Reason: insight.ReasonCompleted, ReasonText: "Completed", FinishedAt: base.Add(-41 * time.Minute)},
		}}
	case strings.HasPrefix(p.Name, "metrics-server"):
		app := running("app", p.Image, 0)
		app.Ready = insight.TruthFalse
		ready.Status = insight.TruthFalse
		obs.Containers = []insight.ContainerObservation{app}
	default:
		obs.Containers = []insight.ContainerObservation{running("app", p.Image, int32(p.Restarts))}
	}
	obs.Conditions = []insight.Condition{ready}
	return obs
}
