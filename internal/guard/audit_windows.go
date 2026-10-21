package guard

import "os"

const auditNoFollow = 0

func private(os.FileInfo, bool) bool { return false }

func lockAudit(*os.File) error { return AuditFailed }
