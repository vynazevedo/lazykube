package kube

import (
	"context"
	"encoding/json"

	"github.com/vynazevedo/lazykube/internal/guard"
	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

type actionAuthorizer struct{ client kubernetes.Interface }

func (a actionAuthorizer) Allowed(ctx context.Context, r guard.Access) (bool, error) {
	if r.Resource == "nodes" && r.Subresource == "proxy" {
		return false, guard.Unsupported
	}
	review, err := a.client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authorizationv1.ResourceAttributes{Group: r.Group, Version: r.Version, Resource: r.Resource, Subresource: r.Subresource, Namespace: r.Namespace, Name: r.Name, Verb: r.Verb}}}, metav1.CreateOptions{})
	if err != nil {
		return false, guard.Unavailable
	}
	return review.Status.Allowed && !review.Status.Denied && review.Status.EvaluationError == "", nil
}

type ActiveAccess interface {
	Run(context.Context, guard.Request) error
}
type actionBackend struct {
	client dynamic.Interface
	active ActiveAccess
}

func (b actionBackend) Supported(a guard.Action) bool {
	return a >= guard.Delete && a <= guard.Edit || a == guard.Cordon || a == guard.Uncordon || (a == guard.Exec || a == guard.PortForward) && b.active != nil
}
func (b actionBackend) resource(t guard.Target) dynamic.ResourceInterface {
	r := b.client.Resource(schema.GroupVersionResource{Group: t.Group, Version: t.Version, Resource: t.Resource})
	if t.Namespace != "" {
		return r.Namespace(t.Namespace)
	}
	return r
}
func actionSubresource(r guard.Request) []string {
	if r.Action == guard.Scale {
		return []string{"scale"}
	}
	return nil
}
func readReason(err error) guard.Reason {
	switch {
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		return guard.Denied
	case apierrors.IsNotFound(err):
		return guard.Stale
	}
	return guard.Unavailable
}

func patches(a guard.Action) bool {
	return a == guard.Restart || a == guard.Cordon || a == guard.Uncordon
}

func unchanged(r guard.Request, obj *unstructured.Unstructured) bool {
	switch r.Action {
	case guard.Scale:
		return scaleReplicas(obj) == int64(r.Parameters.Replicas)
	case guard.Cordon, guard.Uncordon:
		return nodeUnschedulable(obj) == (r.Action == guard.Cordon)
	}
	return false
}

func scaleReplicas(obj *unstructured.Unstructured) int64 {
	n, _, _ := unstructured.NestedInt64(obj.Object, "spec", "replicas")
	return n
}

func nodeUnschedulable(obj *unstructured.Unstructured) bool {
	v, _, _ := unstructured.NestedBool(obj.Object, "spec", "unschedulable")
	return v
}

func nodePatch(r guard.Request) ([]byte, error) {
	return json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": r.Target.UID},
		{"op": "test", "path": "/metadata/resourceVersion", "value": r.Target.ResourceVersion},
		{"op": "add", "path": "/spec/unschedulable", "value": r.Action == guard.Cordon},
	})
}

func (b actionBackend) Observe(ctx context.Context, action guard.Action, t guard.Target) (guard.Observation, error) {
	obj, err := b.resource(t).Get(ctx, t.Name, metav1.GetOptions{}, actionSubresource(guard.Request{Action: action})...)
	if err != nil {
		return guard.Observation{}, readReason(err)
	}
	out := guard.Observation{Target: t, Finalizers: len(obj.GetFinalizers()), Terminating: obj.GetDeletionTimestamp() != nil}
	out.Target.UID, out.Target.ResourceVersion = string(obj.GetUID()), obj.GetResourceVersion()
	if owner := metav1.GetControllerOf(obj); owner != nil {
		out.Owner = owner.Kind + "/" + owner.Name
	}
	switch action {
	case guard.Scale:
		observed, _, _ := unstructured.NestedInt64(obj.Object, "status", "replicas")
		out.Replicas, out.ObservedReplicas = int32(scaleReplicas(obj)), int32(observed)
	case guard.Restart:
		path := []string{"spec", "updateStrategy", "type"}
		if t.Resource == "deployments" {
			path = []string{"spec", "strategy", "type"}
		}
		out.Strategy, _, _ = unstructured.NestedString(obj.Object, path...)
	case guard.Delete:
		if t.Group == "" && t.Resource == "pods" {
			out.GraceSeconds, out.GraceKnown, _ = unstructured.NestedInt64(obj.Object, "spec", "terminationGracePeriodSeconds")
		}
	case guard.Cordon, guard.Uncordon:
		out.Unschedulable = nodeUnschedulable(obj)
	}
	return out, nil
}

func (b actionBackend) Validate(ctx context.Context, r guard.Request) error {
	obj, err := b.resource(r.Target).Get(ctx, r.Target.Name, metav1.GetOptions{}, actionSubresource(r)...)
	if err != nil {
		return readReason(err)
	}
	if string(obj.GetUID()) != r.Target.UID || obj.GetResourceVersion() != r.Target.ResourceVersion {
		return guard.Stale
	}
	if unchanged(r, obj) {
		return guard.Unchanged
	}
	if r.Action == guard.Exec {
		containers, _, _ := unstructured.NestedSlice(obj.Object, "spec", "containers")
		init, _, _ := unstructured.NestedSlice(obj.Object, "spec", "initContainers")
		ephemeral, _, _ := unstructured.NestedSlice(obj.Object, "spec", "ephemeralContainers")
		found := false
		for _, c := range append(append(containers, init...), ephemeral...) {
			entry, ok := c.(map[string]any)
			if ok && entry["name"] == r.Parameters.Container {
				found = true
			}
		}
		if !found {
			return guard.Stale
		}
	}
	return nil
}
func (b actionBackend) Send(ctx context.Context, r guard.Request, dry bool) error {
	var dryRun []string
	if dry {
		dryRun = []string{metav1.DryRunAll}
	}
	client := b.resource(r.Target)
	var err error
	switch r.Action {
	case guard.Delete:
		uid, rv := types.UID(r.Target.UID), r.Target.ResourceVersion
		propagation := metav1.DeletionPropagation(r.Parameters.Propagation)
		err = client.Delete(ctx, r.Target.Name, metav1.DeleteOptions{DryRun: dryRun, Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}, GracePeriodSeconds: &r.Parameters.GraceSeconds, PropagationPolicy: &propagation})
	case guard.Scale:
		obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "autoscaling/v1", "kind": "Scale", "metadata": map[string]any{"name": r.Target.Name, "namespace": r.Target.Namespace, "uid": r.Target.UID, "resourceVersion": r.Target.ResourceVersion}, "spec": map[string]any{"replicas": int64(r.Parameters.Replicas)}}}
		_, err = client.Update(ctx, obj, metav1.UpdateOptions{DryRun: dryRun}, "scale")
	case guard.Restart:
		obj, getErr := client.Get(ctx, r.Target.Name, metav1.GetOptions{})
		if getErr != nil {
			return guard.Unavailable
		}
		annotations, _, _ := unstructured.NestedStringMap(obj.Object, "spec", "template", "metadata", "annotations")
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations["kubectl.kubernetes.io/restartedAt"] = string(r.Parameters.Document)
		patch := []map[string]any{{"op": "test", "path": "/metadata/uid", "value": r.Target.UID}, {"op": "test", "path": "/metadata/resourceVersion", "value": r.Target.ResourceVersion}, {"op": "add", "path": "/spec/template/metadata/annotations", "value": annotations}}
		body, marshalErr := json.Marshal(patch)
		if marshalErr != nil {
			return guard.Invalid
		}
		_, err = client.Patch(ctx, r.Target.Name, types.JSONPatchType, body, metav1.PatchOptions{DryRun: dryRun})
	case guard.Cordon, guard.Uncordon:
		body, marshalErr := nodePatch(r)
		if marshalErr != nil {
			return guard.Invalid
		}
		_, err = client.Patch(ctx, r.Target.Name, types.JSONPatchType, body, metav1.PatchOptions{DryRun: dryRun})
	case guard.Edit:
		obj := &unstructured.Unstructured{}
		if decodeErr := obj.UnmarshalJSON(r.Parameters.Document); decodeErr != nil {
			return guard.Invalid
		}
		if obj.GetName() != r.Target.Name || obj.GetNamespace() != r.Target.Namespace || string(obj.GetUID()) != r.Target.UID || obj.GetResourceVersion() != r.Target.ResourceVersion {
			return guard.Stale
		}
		_, err = client.Update(ctx, obj, metav1.UpdateOptions{DryRun: dryRun, FieldManager: "lazykube"})
	case guard.Exec, guard.PortForward:
		if dry || b.active == nil {
			return guard.Unsupported
		}
		return b.active.Run(ctx, r)
	default:
		return guard.Unsupported
	}
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
		return guard.Denied
	}
	if apierrors.IsConflict(err) || apierrors.IsNotFound(err) || patches(r.Action) && apierrors.IsInvalid(err) {
		return guard.Stale
	}
	if err != nil {
		return guard.Unknown
	}
	return nil
}

func NewActionExecutor(ctx context.Context, s guard.Session, allowWrites bool, client kubernetes.Interface, dc dynamic.Interface, audit guard.Auditor, clock guard.Clock, active ActiveAccess) (*guard.Executor, error) {
	if client == nil || dc == nil {
		return nil, guard.Invalid
	}
	return guard.NewExecutor(ctx, s, guard.NewPolicy(allowWrites), actionAuthorizer{client: client}, actionBackend{client: dc, active: active}, audit, clock)
}

func (s *Source) Actions() *guard.Executor { return s.actions }
func (s *Source) AccessMode() guard.Mode   { return s.guardPolicy.Mode(s.guardSession) }
func (s *Source) GuardSession() guard.Session {
	return s.guardSession
}
