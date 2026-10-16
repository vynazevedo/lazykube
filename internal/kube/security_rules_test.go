package kube

import (
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/vynazevedo/lazykube/internal/data"
)

func hardenedPod() *corev1.Pod {
	p := testPod("apps", "safe", "", corev1.PodRunning)
	p.ResourceVersion = "1"
	p.Spec.SecurityContext = &corev1.PodSecurityContext{RunAsUser: ptr(int64(1000)), RunAsNonRoot: ptr(true), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}
	p.Spec.Containers[0].Image = "registry:5000/app:latest@sha256:" + strings.Repeat("a", 64)
	p.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{AllowPrivilegeEscalation: ptr(false), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}}
	return p
}

func rule(posture data.PodPosture, name string) (data.Finding, bool) {
	for _, f := range posture.Findings {
		if f.Rule == name {
			return f, true
		}
	}
	return data.Finding{}, false
}

func TestSecurityRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*corev1.Pod)
		rule   string
		field  string
		result data.RuleResult
	}{
		{"privileged", func(p *corev1.Pod) { p.Spec.Containers[0].SecurityContext.Privileged = ptr(true) }, "privileged", "spec.containers[0].securityContext.privileged", data.ResultFinding},
		{"host network", func(p *corev1.Pod) { p.Spec.HostNetwork = true }, "hostNetwork", "spec.hostNetwork", data.ResultFinding},
		{"host pid", func(p *corev1.Pod) { p.Spec.HostPID = true }, "hostPID", "spec.hostPID", data.ResultFinding},
		{"host ipc", func(p *corev1.Pod) { p.Spec.HostIPC = true }, "hostIPC", "spec.hostIPC", data.ResultFinding},
		{"host path", func(p *corev1.Pod) {
			p.Spec.Volumes = []corev1.Volume{{VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/private-sentinel"}}}}
		}, "hostPath", "spec.volumes[0].hostPath", data.ResultFinding},
		{"root override", func(p *corev1.Pod) { p.Spec.Containers[0].SecurityContext.RunAsUser = ptr(int64(0)) }, "runAsRoot", "spec.containers[0].securityContext.runAsUser", data.ResultFinding},
		{"root inherited", func(p *corev1.Pod) { p.Spec.SecurityContext.RunAsUser = ptr(int64(0)) }, "runAsRoot", "spec.securityContext.runAsUser", data.ResultFinding},
		{"uid absent", func(p *corev1.Pod) { p.Spec.SecurityContext.RunAsUser = nil }, "runAsRoot", "spec.securityContext.runAsUser", data.ResultUnknown},
		{"nonroot override", func(p *corev1.Pod) { p.Spec.Containers[0].SecurityContext.RunAsNonRoot = ptr(false) }, "runAsNonRoot", "spec.containers[0].securityContext.runAsNonRoot", data.ResultFinding},
		{"nonroot absent", func(p *corev1.Pod) { p.Spec.SecurityContext.RunAsNonRoot = nil }, "runAsNonRoot", "spec.securityContext.runAsNonRoot", data.ResultFinding},
		{"seccomp absent", func(p *corev1.Pod) { p.Spec.SecurityContext.SeccompProfile = nil }, "seccomp", "spec.securityContext.seccompProfile", data.ResultFinding},
		{"seccomp override", func(p *corev1.Pod) {
			p.Spec.Containers[0].SecurityContext.SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeUnconfined}
		}, "seccomp", "spec.containers[0].securityContext.seccompProfile", data.ResultFinding},
		{"escalation", func(p *corev1.Pod) { p.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation = ptr(true) }, "allowPrivilegeEscalation", "spec.containers[0].securityContext.allowPrivilegeEscalation", data.ResultFinding},
		{"escalation absent", func(p *corev1.Pod) { p.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation = nil }, "allowPrivilegeEscalation", "spec.containers[0].securityContext.allowPrivilegeEscalation", data.ResultFinding},
		{"capability", func(p *corev1.Pod) {
			p.Spec.Containers[0].SecurityContext.Capabilities.Add = []corev1.Capability{"SYS_ADMIN"}
		}, "capabilitiesAdded", "spec.containers[0].securityContext.capabilities.add", data.ResultFinding},
		{"no drop", func(p *corev1.Pod) { p.Spec.Containers[0].SecurityContext.Capabilities = nil }, "capabilitiesDrop", "spec.containers[0].securityContext.capabilities.drop", data.ResultFinding},
		{"memory missing", func(p *corev1.Pod) { delete(p.Spec.Containers[0].Resources.Limits, corev1.ResourceMemory) }, "limit-memory", "spec.containers[0].resources.limits.memory", data.ResultFinding},
		{"cpu zero", func(p *corev1.Pod) {
			p.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU] = resource.MustParse("0")
		}, "limit-cpu", "spec.containers[0].resources.limits.cpu", data.ResultFinding},
		{"windows", func(p *corev1.Pod) { p.Spec.OS = &corev1.PodOS{Name: corev1.Windows} }, "linuxSecurityContext", "spec.containers[0].securityContext", data.ResultNotApplicable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := hardenedPod()
			tc.mutate(p)
			before := p.DeepCopy()
			got := evaluatePod(p, "test", 7, fixedNow)
			f, ok := rule(got, tc.rule)
			if !ok || f.Field != tc.field || f.Result != tc.result || f.Target.Generation != 7 || f.RuleVersion == "" || f.Recommendation == "" || f.ObservedAt != fixedNow {
				t.Fatalf("unexpected finding: %+v", f)
			}
			if strings.Contains(f.Evidence, "private-sentinel") {
				t.Fatal("raw volume path leaked")
			}
			if !reflect.DeepEqual(p, before) {
				t.Fatal("input changed")
			}
		})
	}
}

func TestSecurityInheritanceAndContainerKinds(t *testing.T) {
	p := hardenedPod()
	if got := evaluatePod(p, "test", 1, fixedNow); len(got.Findings) != 0 {
		t.Fatalf("hardened pod: %+v", got)
	}
	p.Spec.SecurityContext.RunAsUser = ptr(int64(0))
	p.Spec.Containers[0].SecurityContext.RunAsUser = ptr(int64(2000))
	if _, ok := rule(evaluatePod(p, "test", 1, fixedNow), "runAsRoot"); ok {
		t.Fatal("container override ignored")
	}
	p.Spec.InitContainers = []corev1.Container{{Name: "init", SecurityContext: &corev1.SecurityContext{Privileged: ptr(true)}}}
	p.Spec.EphemeralContainers = []corev1.EphemeralContainer{{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "debug", SecurityContext: &corev1.SecurityContext{Privileged: ptr(true)}}}}
	got := evaluatePod(p, "test", 1, fixedNow)
	seen := map[string]bool{}
	for _, f := range got.Findings {
		if f.Rule == "privileged" {
			seen[f.Container] = true
		}
		if strings.HasPrefix(f.Field, "spec.ephemeralContainers") && strings.HasPrefix(f.Rule, "limit-") {
			t.Fatal("limits incorrectly required for ephemeral container")
		}
	}
	if !seen["init"] || !seen["debug"] {
		t.Fatalf("containers missing: %v", seen)
	}
}

func TestSecurityImages(t *testing.T) {
	for _, tc := range []struct {
		image          string
		latest, digest bool
	}{
		{"app", true, true}, {"app:latest", true, true}, {"registry:5000/app", true, true},
		{"registry:5000/app:v1", false, true}, {"app:latest@sha256:" + strings.Repeat("a", 64), false, false},
		{"registry:5000/app@sha256:" + strings.Repeat("b", 64), false, false},
	} {
		p := hardenedPod()
		p.Spec.Containers[0].Image = tc.image
		got := evaluatePod(p, "test", 1, fixedNow)
		_, latest := rule(got, "imageLatest")
		_, digest := rule(got, "imageDigest")
		if latest != tc.latest || digest != tc.digest {
			t.Fatalf("%s: latest=%t digest=%t", tc.image, latest, digest)
		}
	}
}

func TestSecurityPodLimitsAndMissingIdentity(t *testing.T) {
	p := hardenedPod()
	p.Spec.Resources = &corev1.ResourceRequirements{Limits: p.Spec.Containers[0].Resources.Limits.DeepCopy()}
	p.Spec.Containers[0].Resources.Limits = nil
	got := evaluatePod(p, "test", 1, fixedNow)
	if len(got.Findings) != 0 {
		t.Fatalf("pod limits ignored: %+v", got.Findings)
	}
	p.UID = ""
	p.Spec.Resources.Limits[corev1.ResourceCPU] = resource.MustParse("0")
	got = evaluatePod(p, "test", 1, fixedNow)
	if got.Coverage.State != data.CoveragePartial {
		t.Fatal("missing identity not marked partial")
	}
	f, ok := rule(got, "limit-cpu")
	if !ok || f.Field != "spec.resources.limits.cpu" || !strings.Contains(f.Evidence, "non-positive") {
		t.Fatalf("pod limit: %+v", f)
	}
}

func BenchmarkEvaluatePod(b *testing.B) {
	p := hardenedPod()
	b.ReportAllocs()
	for b.Loop() {
		evaluatePod(p, "test", 1, fixedNow)
	}
}

func TestSecurityMissingContextsAndFindingLimit(t *testing.T) {
	p := hardenedPod()
	p.Spec.SecurityContext = nil
	p.Spec.Containers[0].SecurityContext = nil
	got := evaluatePod(p, "test", 1, fixedNow)
	for _, name := range []string{"runAsRoot", "runAsNonRoot", "seccomp", "allowPrivilegeEscalation", "capabilitiesDrop"} {
		if _, ok := rule(got, name); !ok {
			t.Fatalf("missing %s", name)
		}
	}
	p.Spec.Containers = make([]corev1.Container, 100)
	got = evaluatePod(p, "test", 1, fixedNow)
	if len(got.Findings) != maxPodFindings || got.Coverage.State != data.CoveragePartial || got.Coverage.Reason == "" {
		t.Fatal("unbounded or silently truncated findings")
	}
}

func TestImageDigestValidation(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"sha256:" + strings.Repeat("a", 64), true}, {"sha512:" + strings.Repeat("0", 128), true},
		{"sha256:" + strings.Repeat("z", 64), false}, {"sha256:abc", false}, {"", false}, {"sha1:" + strings.Repeat("a", 40), false},
	} {
		if validImageDigest(tc.value) != tc.valid {
			t.Fatalf("invalid classification: %q", tc.value)
		}
	}
	p := hardenedPod()
	p.Spec.Containers[0].Image = "app:latest@invalid"
	got := evaluatePod(p, "test", 1, fixedNow)
	if f, ok := rule(got, "imageReference"); !ok || f.Result != data.ResultUnknown {
		t.Fatal("invalid digest treated as pinned")
	}
}

func TestSysAdminForcesEscalationFinding(t *testing.T) {
	p := hardenedPod()
	p.Spec.Containers[0].SecurityContext.Capabilities.Add = []corev1.Capability{"SYS_ADMIN"}
	if _, ok := rule(evaluatePod(p, "test", 1, fixedNow), "allowPrivilegeEscalation"); !ok {
		t.Fatal("SYS_ADMIN must override escalation false")
	}
}
