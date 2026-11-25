package guard

import "os"

const (
	auditNoFollow  = 0
	auditReadFlags = 0
)

func openAuditDir(string) (*os.File, error) { return nil, AuditFailed }

func private(os.FileInfo, bool) bool { return false }

func lockAudit(*os.File) error { return AuditFailed }
