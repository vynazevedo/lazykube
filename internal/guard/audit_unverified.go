//go:build !linux

package guard

import "os"

const auditFileFlags = 0

func walkAudit(string, bool) (*os.File, error) { return nil, RefusedPlatform }

func trustedFilesystem(*os.File) bool { return false }

func private(os.FileInfo, bool) bool { return false }

func lockAudit(*os.File) error { return AuditFailed }
