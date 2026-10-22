package kube

import (
	"strings"
	"unicode"
	"unsafe"

	"github.com/vynazevedo/lazykube/internal/data"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func projectResource(object metav1.Object) (data.ResourceRow, bool) {
	name, namespace, uid, rv := object.GetName(), object.GetNamespace(), string(object.GetUID()), object.GetResourceVersion()
	if name == "" || uid == "" || rv == "" || len(name) > 253 || len(namespace) > 63 || len(uid) > 128 || len(rv) > 128 {
		return data.ResourceRow{}, false
	}
	for _, v := range []string{name, namespace, uid, rv} {
		for _, r := range v {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.IsSpace(r) || r == 0xfffd {
				return data.ResourceRow{}, false
			}
		}
	}
	return data.ResourceRow{Ref: data.ResourceMetadataRef{Name: strings.Clone(name), Namespace: strings.Clone(namespace), UID: strings.Clone(uid), ResourceVersion: strings.Clone(rv), CreatedAt: object.GetCreationTimestamp().Time}}, true
}

func resourceRowFits(row data.ResourceRow) bool {
	return int(unsafe.Sizeof(row))+len(row.Ref.Name)+len(row.Ref.Namespace)+len(row.Ref.UID)+len(row.Ref.ResourceVersion)+len(row.Workload.Schedule)+len(row.Workload.Reason) <= 1024
}
