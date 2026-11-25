package kube

import (
	"reflect"
	"unsafe"

	"github.com/vynazevedo/lazykube/internal/data"
)

const mapSlack = 3

func allocationSize(n int) int {
	if n == 0 {
		return 0
	}
	return (n + n/4 + 15) &^ 15
}

func retainedBytes(v reflect.Value) int {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return 0
		}
		return allocationSize(int(v.Elem().Type().Size())) + retainedBytes(v.Elem())
	case reflect.Struct:
		total := 0
		for i := range v.NumField() {
			total += retainedBytes(v.Field(i))
		}
		return total
	case reflect.Slice:
		total := allocationSize(v.Cap() * int(v.Type().Elem().Size()))
		for i := range v.Len() {
			total += retainedBytes(v.Index(i))
		}
		return total
	case reflect.String:
		return allocationSize(v.Len())
	}
	return 0
}

func rowBytes(row data.ResourceRow) int {
	slot := int(unsafe.Sizeof("") + unsafe.Sizeof(row))
	total := mapSlack * slot
	for _, s := range []string{row.Ref.Namespace + "/" + row.Ref.Name, row.Ref.Namespace, row.Ref.Name, row.Ref.UID, row.Ref.ResourceVersion, row.Workload.Schedule, row.Workload.Reason} {
		total += allocationSize(len(s))
	}
	return total
}
