package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/insight"
)

const (
	rebuildEvery   = 250 * time.Millisecond
	ageRefresh     = 15 * time.Second
	eventsLimit    = 50
	involvedIndex  = "involvedPod"
	defaultTimeout = 20 * time.Second
)

var ErrSyncTimeout = errors.New("timed out waiting for the cluster cache to sync")

type Options struct {
	Context     string
	Namespace   string
	SyncTimeout time.Duration
	Now         func() time.Time
	Location    *time.Location
	Progress    func(data.Progress)
}

type caps struct {
	deployments bool
	nodes       bool
	namespaces  bool
	events      bool
	metrics     bool
}

type Source struct {
	observations       *insight.Inventory
	observationPods    map[string]insight.PodObservation
	securityObjects    map[string]*corev1.Pod
	security           data.SecuritySnapshot
	generation         uint64
	podCoverage        data.Coverage
	deploymentCoverage data.Coverage

	client  kubernetes.Interface
	metrics metricsclient.Interface
	rest    rest.Interface
	version discovery.ServerVersionInterface
	opts    Options
	caps    caps
	server  string

	netTimeout time.Duration
	manualPoll bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	factory    informers.SharedInformerFactory
	clusterFac informers.SharedInformerFactory
	podInf     cache.SharedIndexInformer
	deployInf  cache.SharedIndexInformer
	nsInf      cache.SharedIndexInformer
	nodeInf    cache.SharedIndexInformer
	eventInf   cache.SharedIndexInformer
	wide       *wideEvents
	dirty      chan struct{}
	changes    chan struct{}
	pollNow    chan struct{}
	closeOnce  sync.Once
	closedChan chan struct{}
	notifyMu   sync.Mutex
	closed     bool

	mu       sync.RWMutex
	snap     data.Snapshot
	byKey    map[string]*corev1.Pod
	nodes    map[string]*corev1.Node
	nodeList []string
	active   string
	usage    usage
	stream   *logStream
	yamlKey  string
	yamlOut  []string
}

func newSource(client kubernetes.Interface, mc metricsclient.Interface, opts Options) *Source {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.SyncTimeout <= 0 {
		opts.SyncTimeout = defaultTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Source{
		generation:         securityGeneration.Add(1),
		observations:       insight.NewInventory(insight.ClockFunc(opts.Now)),
		observationPods:    make(map[string]insight.PodObservation),
		podCoverage:        data.Coverage{State: data.CoverageCollecting},
		deploymentCoverage: data.Coverage{State: data.CoverageCollecting},

		client:     client,
		metrics:    mc,
		opts:       opts,
		ctx:        ctx,
		cancel:     cancel,
		dirty:      make(chan struct{}, 1),
		changes:    make(chan struct{}, 1),
		pollNow:    make(chan struct{}, 1),
		closedChan: make(chan struct{}),
		byKey:      map[string]*corev1.Pod{},
		nodes:      map[string]*corev1.Node{},
		usage:      newUsage(),
		netTimeout: requestTimeout,
	}
	s.publishObservations(nil)
	if client != nil {
		s.rest = client.CoreV1().RESTClient()
		s.version = client.Discovery()
	}
	return s
}

func (s *Source) start(ctx context.Context) error {
	if err := s.probe(ctx); err != nil {
		return err
	}
	ns := s.opts.Namespace
	s.factory = informers.NewSharedInformerFactoryWithOptions(s.client, 0,
		informers.WithNamespace(ns),
		informers.WithTransform(stripManagedFields))
	s.podInf = s.newSecurityInformer(false)
	if err := s.podInf.SetWatchErrorHandler(s.watchSecurityError(false)); err != nil {
		return err
	}
	synced := []cache.InformerSynced{s.podInf.HasSynced}
	resources := []string{"pods"}
	if s.caps.deployments {
		s.deployInf = s.newSecurityInformer(true)
		if err := s.deployInf.SetWatchErrorHandler(s.watchSecurityError(true)); err != nil {
			return err
		}
		resources = append(resources, "deployments (optional)")
	}
	if s.caps.events {
		s.eventInf = s.newEventsInformer()
		if err := s.eventInf.AddIndexers(cache.Indexers{involvedIndex: involvedPodIndex}); err != nil {
			return err
		}
		synced = append(synced, s.eventInf.HasSynced)
		resources = append(resources, "events")
	}
	cluster := informers.NewSharedInformerFactoryWithOptions(s.client, 0, informers.WithTransform(stripManagedFields))
	s.clusterFac = cluster
	if s.caps.namespaces {
		s.nsInf = cluster.Core().V1().Namespaces().Informer()
		synced = append(synced, s.nsInf.HasSynced)
		resources = append(resources, "namespaces")
	}
	if s.caps.nodes {
		s.nodeInf = cluster.Core().V1().Nodes().Informer()
		synced = append(synced, s.nodeInf.HasSynced)
		resources = append(resources, "nodes")
	}
	for _, inf := range []cache.SharedIndexInformer{s.podInf, s.deployInf, s.nsInf, s.nodeInf, s.eventInf} {
		if inf == nil {
			continue
		}
		if _, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    func(any) { s.markDirty() },
			UpdateFunc: func(any, any) { s.markDirty() },
			DeleteFunc: func(any) { s.markDirty() },
		}); err != nil {
			return err
		}
	}
	s.report(data.StageSync, false, strings.Join(resources, ", "))
	s.factory.Start(s.ctx.Done())
	cluster.Start(s.ctx.Done())

	syncCtx, cancel := context.WithTimeout(ctx, s.opts.SyncTimeout)
	defer cancel()
	if !cache.WaitForCacheSync(syncCtx.Done(), synced...) {
		if err := ctx.Err(); err != nil {
			return err
		}
		return ErrSyncTimeout
	}
	s.rebuild()
	snap := s.Snapshot()
	s.report(data.StageSync, true, fmt.Sprintf("%d pods, %d deployments", len(snap.Pods), len(snap.Deployments)))
	s.wg.Add(1)
	go s.rebuildLoop()
	if !s.manualPoll {
		s.wg.Add(1)
		go s.pollLoop()
	}
	return nil
}

func (s *Source) report(stage data.Stage, done bool, detail string) {
	report(s.opts.Progress, data.Progress{Stage: stage, Done: done, Context: s.opts.Context, Detail: detail})
}

type restDiscovery interface {
	RESTClient() rest.Interface
}

func serverVersion(ctx context.Context, v discovery.ServerVersionInterface) (*version.Info, error) {
	if rd, ok := v.(restDiscovery); ok {
		if rc := rd.RESTClient(); rc != nil {
			raw, err := rc.Get().AbsPath("/version").Do(ctx).Raw()
			if err != nil {
				return nil, err
			}
			var info version.Info
			if err := json.Unmarshal(raw, &info); err != nil {
				return nil, fmt.Errorf("invalid /version response: %w", err)
			}
			return &info, nil
		}
	}
	type result struct {
		info *version.Info
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		info, err := v.ServerVersion()
		ch <- result{info, err}
	}()
	select {
	case r := <-ch:
		return r.info, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Source) probe(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.opts.SyncTimeout)
	defer cancel()
	v, err := serverVersion(ctx, s.version)
	if err != nil {
		return fmt.Errorf("cannot reach the API server: %w", err)
	}
	s.server = v.GitVersion
	s.report(data.StageConnect, true, v.GitVersion)
	s.report(data.StagePermissions, false, "")
	one := metav1.ListOptions{Limit: 1}
	ns := s.opts.Namespace
	if _, err := s.client.CoreV1().Pods(ns).List(ctx, one); err != nil {
		return fmt.Errorf("cannot list pods: %w", err)
	}
	deploymentCtx, cancelDeployments := context.WithTimeout(ctx, min(s.opts.SyncTimeout/4, 2*time.Second))
	_, deploymentErr := s.client.AppsV1().Deployments(ns).List(deploymentCtx, one)
	cancelDeployments()
	if err := deploymentErr; err != nil {
		s.deploymentCoverage = data.Coverage{State: data.CoverageUnavailable, Reason: "deployments unavailable"}
		if errors.Is(err, context.DeadlineExceeded) || apierrors.IsTimeout(err) {
			s.deploymentCoverage.Reason = "timeout: deployments"
		}
		if apierrors.IsForbidden(err) {
			s.deploymentCoverage = data.Coverage{State: data.CoverageDenied, Reason: "no permission to list deployments"}
		}
	} else {
		s.caps.deployments = true
	}
	allowed := func(err error) (bool, error) {
		if err == nil {
			return true, nil
		}
		if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) || apierrors.IsMethodNotSupported(err) {
			return false, nil
		}
		return false, err
	}
	var errs []error
	_, err = s.client.CoreV1().Events(ns).List(ctx, one)
	ok, err := allowed(err)
	s.caps.events, errs = ok, append(errs, err)
	_, err = s.client.CoreV1().Nodes().List(ctx, one)
	ok, err = allowed(err)
	s.caps.nodes, errs = ok, append(errs, err)
	if ns == "" {
		_, err = s.client.CoreV1().Namespaces().List(ctx, one)
		ok, err = allowed(err)
		s.caps.namespaces, errs = ok, append(errs, err)
	}
	if s.metrics != nil {
		_, err = s.metrics.MetricsV1beta1().PodMetricses(ns).List(ctx, one)
		s.caps.metrics = err == nil
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	s.report(data.StagePermissions, true, s.caps.summary(ns == "", s.metrics != nil))
	return nil
}

func (c caps) summary(clusterWide, metricsClient bool) string {
	var missing []string
	if !c.deployments {
		missing = append(missing, "deployments")
	}
	if !c.events {
		missing = append(missing, "events")
	}
	if !c.nodes {
		missing = append(missing, "nodes")
	}
	if clusterWide && !c.namespaces {
		missing = append(missing, "namespaces")
	}
	if metricsClient && !c.metrics {
		missing = append(missing, "metrics")
	}
	if len(missing) == 0 {
		return "acesso completo"
	}
	return "sem acesso: " + strings.Join(missing, ", ")
}

func stripManagedFields(obj any) (any, error) {
	if acc, err := meta.Accessor(obj); err == nil {
		acc.SetManagedFields(nil)
	}
	return obj, nil
}

func involvedPodIndex(obj any) ([]string, error) {
	e, ok := obj.(*corev1.Event)
	if !ok || e.InvolvedObject.Kind != "Pod" {
		return nil, nil
	}
	return []string{podKey(e.InvolvedObject.Namespace, e.InvolvedObject.Name)}, nil
}

func (s *Source) markDirty() {
	select {
	case s.dirty <- struct{}{}:
	default:
	}
}

func (s *Source) notify() {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.changes <- struct{}{}:
	default:
	}
}

func (s *Source) Changes() <-chan struct{} {
	return s.changes
}

func (s *Source) Close() {
	s.closeOnce.Do(func() {
		s.cancel()
		s.mu.Lock()
		if s.stream != nil {
			s.stream.cancel()
			s.stream = nil
		}
		s.mu.Unlock()
		for _, f := range []informers.SharedInformerFactory{s.factory, s.clusterFac} {
			if f != nil {
				f.Shutdown()
			}
		}
		s.wg.Wait()
		s.shutdownWideEvents()
		s.notifyMu.Lock()
		s.closed = true
		close(s.changes)
		s.notifyMu.Unlock()
		close(s.closedChan)
	})
}

func (s *Source) rebuildLoop() {
	defer s.wg.Done()
	ages := time.NewTicker(ageRefresh)
	defer ages.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.dirty:
		case <-ages.C:
		}
		s.rebuild()
		s.notify()
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(rebuildEvery):
		}
	}
}

func listOf[T any](inf cache.SharedIndexInformer) []*T {
	if inf == nil {
		return nil
	}
	items := inf.GetStore().List()
	out := make([]*T, 0, len(items))
	for _, it := range items {
		if v, ok := it.(*T); ok {
			out = append(out, v)
		}
	}
	return out
}

func (s *Source) rebuild() {
	now := s.opts.Now()
	pods := listOf[corev1.Pod](s.podInf)
	sort.Slice(pods, func(i, j int) bool {
		if pods[i].Namespace != pods[j].Namespace {
			return pods[i].Namespace < pods[j].Namespace
		}
		return pods[i].Name < pods[j].Name
	})
	securityNS := listOf[corev1.Namespace](s.nsInf)
	names := make([]data.Namespace, 0, len(securityNS)+1)
	for _, ns := range securityNS {
		names = append(names, data.Namespace{Name: ns.Name})
	}
	if s.opts.Namespace != "" {
		names = append(names, data.Namespace{Name: s.opts.Namespace})
	}
	s.rebuildSecurity(pods, names, now)
	byKey := make(map[string]*corev1.Pod, len(pods))
	outPods := make([]data.Pod, 0, len(pods))
	perNS := map[string]int{}
	for _, p := range pods {
		byKey[podKey(p.Namespace, p.Name)] = p
		outPods = append(outPods, toPod(p, now))
		perNS[p.Namespace]++
	}

	deploys := listOf[appsv1.Deployment](s.deployInf)
	outDeploys := make([]data.Deployment, len(deploys))
	for i, d := range deploys {
		outDeploys[i] = toDeployment(d)
	}
	sort.Slice(outDeploys, func(i, j int) bool {
		if outDeploys[i].Namespace != outDeploys[j].Namespace {
			return outDeploys[i].Namespace < outDeploys[j].Namespace
		}
		return outDeploys[i].Name < outDeploys[j].Name
	})

	var outNS []data.Namespace
	uptime := "-"
	if s.nsInf != nil {
		var oldest metav1.Time
		for _, n := range listOf[corev1.Namespace](s.nsInf) {
			outNS = append(outNS, data.Namespace{Name: n.Name, Pods: perNS[n.Name]})
			if oldest.IsZero() || n.CreationTimestamp.Before(&oldest) {
				oldest = n.CreationTimestamp
			}
		}
		uptime = age(oldest, now)
	} else {
		for name, n := range perNS {
			outNS = append(outNS, data.Namespace{Name: name, Pods: n})
		}
		if s.opts.Namespace != "" && perNS[s.opts.Namespace] == 0 {
			outNS = append(outNS, data.Namespace{Name: s.opts.Namespace})
		}
	}
	sort.Slice(outNS, func(i, j int) bool { return outNS[i].Name < outNS[j].Name })

	nodes := map[string]*corev1.Node{}
	var nodeList []string
	for _, n := range listOf[corev1.Node](s.nodeInf) {
		nodes[n.Name] = n
		nodeList = append(nodeList, n.Name)
	}
	sort.Strings(nodeList)
	nodeCount := len(nodeList)
	if s.nodeInf == nil {
		seen := map[string]bool{}
		for _, p := range pods {
			if p.Spec.NodeName != "" {
				seen[p.Spec.NodeName] = true
			}
		}
		nodeCount = len(seen)
	}

	snap := data.Snapshot{
		Cluster: data.Cluster{
			Context:    s.opts.Context,
			Version:    s.server,
			Nodes:      nodeCount,
			Namespaces: len(outNS),
			Pods:       len(outPods),
			Uptime:     uptime,
		},
		Pods:        outPods,
		Deployments: outDeploys,
		Namespaces:  outNS,
	}

	s.mu.Lock()
	snap.DeploymentCoverage = s.deploymentCoverage
	s.snap = snap
	s.byKey = byKey
	s.publishObservations(pods)
	s.usage.noteEligibility(pods)
	s.nodes = nodes
	s.nodeList = nodeList
	if s.nodeInf != nil {
		for name := range s.usage.net {
			if nodes[name] == nil {
				delete(s.usage.net, name)
			}
		}
	}
	s.mu.Unlock()
}

func (s *Source) Snapshot() data.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := s.snap
	snap.Node = s.nodeLocked(s.displayNodeLocked())
	return snap
}

func (s *Source) displayNodeLocked() string {
	if p := s.byKey[s.active]; p != nil && p.Spec.NodeName != "" {
		return p.Spec.NodeName
	}
	if len(s.nodeList) > 0 {
		return s.nodeList[0]
	}
	for _, p := range s.byKey {
		if p.Spec.NodeName != "" {
			return p.Spec.NodeName
		}
	}
	return ""
}

func (s *Source) nodeLocked(name string) data.Node {
	if name == "" {
		return data.Node{Name: "-", Address: "-", MemUsed: "n/a", MemTotal: "n/a", Arch: "-", Coverage: data.Coverage{State: data.CoverageUnavailable, Reason: reasonNoNode}}
	}
	pods := 0
	for _, p := range s.byKey {
		if p.Spec.NodeName == name && p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed {
			pods++
		}
	}
	n := s.nodes[name]
	if n == nil {
		cov := data.Coverage{State: data.CoverageUnavailable, Reason: reasonNodeUnseen}
		if s.nodeInf == nil {
			cov = data.Coverage{State: data.CoverageDenied, Reason: reasonNodesDeny}
		}
		return data.Node{Name: name, Address: "-", MemUsed: "n/a", MemTotal: "n/a", Pods: pods, Arch: "-", Ready: true, Coverage: cov}
	}
	mem, ok := s.usage.nodeMem[name]
	out := toNode(n, pods, mem, ok)
	out.Coverage = data.Coverage{State: data.CoverageComplete}
	return out
}

func (s *Source) setActive(key string) {
	if key == s.active {
		return
	}
	prevNode := s.displayNodeLocked()
	s.active = key
	if s.displayNodeLocked() != prevNode {
		s.notify()
		select {
		case s.pollNow <- struct{}{}:
		default:
		}
	}
}

func (s *Source) pod(key string) *corev1.Pod {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byKey[key]
}

func (s *Source) Events(key string) []data.Event {
	feed := s.ScopedEvents(data.EventQuery{Scope: data.ScopePod, Pod: key})
	if feed.State == data.FeedForbidden {
		return []data.Event{{Age: "-", Type: "Warning", Reason: "Forbidden", Message: "no permission to list events"}}
	}
	return feed.Items
}

func (s *Source) Manifest(key string) []string {
	p := s.pod(key)
	if p == nil {
		return nil
	}
	version := string(p.UID) + "@" + p.ResourceVersion
	s.mu.RLock()
	cached, out := s.yamlKey == version, s.yamlOut
	s.mu.RUnlock()
	if cached {
		return out
	}
	out = manifest(p)
	s.mu.Lock()
	s.yamlKey, s.yamlOut = version, out
	s.mu.Unlock()
	return out
}

func (s *Source) Describe(key string) []data.Field {
	s.mu.RLock()
	p := s.byKey[key]
	addr := ""
	if p != nil {
		if n := s.nodes[p.Spec.NodeName]; n != nil {
			addr = nodeAddress(n)
		}
	}
	s.mu.RUnlock()
	if p == nil {
		return nil
	}
	return describePod(p, addr)
}
