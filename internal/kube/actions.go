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
	return a >= guard.Delete && a <= guard.Edit || (a == guard.Exec || a == guard.PortForward) && b.active != nil
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
func (b actionBackend) Validate(ctx context.Context, r guard.Request) error {
	obj, err := b.resource(r.Target).Get(ctx, r.Target.Name, metav1.GetOptions{}, actionSubresource(r)...)
	if err != nil {
		return guard.Unavailable
	}
	if string(obj.GetUID()) != r.Target.UID || obj.GetResourceVersion() != r.Target.ResourceVersion {
		return guard.Stale
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
	if apierrors.IsConflict(err) || apierrors.IsNotFound(err) {
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
