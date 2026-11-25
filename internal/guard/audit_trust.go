package guard

import (
	"errors"
	"os"
	"path/filepath"
)

type AuditRefusal string

const (
	RefusedPlatform   AuditRefusal = "privacidade do audit não verificável nesta plataforma"
	RefusedFilesystem AuditRefusal = "filesystem do audit sem dono e modo confiáveis"
	RefusedLink       AuditRefusal = "caminho do audit passa por link não confiável"
	RefusedOwner      AuditRefusal = "caminho do audit pode ser trocado por outro usuário"
	RefusedPrivate    AuditRefusal = "diretório ou arquivo do audit não é privado (0700 e 0600)"
	RefusedForm       AuditRefusal = "caminho do audit não é absoluto e canônico"
)

func (r AuditRefusal) Error() string {
	return string(r)
}

func (r AuditRefusal) Is(target error) bool {
	return target == ErrAuditUnreadable || target == AuditFailed
}

const (
	modeOthersWrite = 0o022
	modeSticky      = 0o1000
)

type auditNode struct {
	uid  uint32
	mode uint32
}

func trustedDir(n auditNode, euid uint32) bool {
	return (n.uid == 0 || n.uid == euid) && (n.mode&modeOthersWrite == 0 || n.mode&modeSticky != 0)
}

func trustedLink(parent, link auditNode) bool {
	return link.uid == 0 && parent.uid == 0 && parent.mode&modeOthersWrite == 0
}

func createdDir(n auditNode, euid uint32) bool {
	return n.uid == euid && n.mode&0o077 == 0
}

type auditDir struct {
	walked *os.File
	root   *os.Root
}

func (d auditDir) close() error {
	return errors.Join(d.root.Close(), d.walked.Close())
}

func openAudit(path string, create bool) (dir auditDir, err error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return dir, RefusedForm
	}
	parent := filepath.Dir(path)
	walked, err := walkAudit(parent, create)
	if err != nil {
		return dir, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, walked.Close())
		}
	}()
	info, err := walked.Stat()
	if err != nil {
		return dir, err
	}
	if !private(info, true) {
		return dir, RefusedPrivate
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return dir, err
	}
	if current, statErr := root.Stat("."); statErr != nil || !os.SameFile(info, current) {
		return dir, errors.Join(RefusedOwner, root.Close())
	}
	return auditDir{walked: walked, root: root}, nil
}

func openAuditFile(root *os.Root, entry string, flags int) (file *os.File, info os.FileInfo, err error) {
	before, err := root.Lstat(entry)
	if err != nil {
		return nil, nil, err
	}
	if !private(before, false) {
		return nil, nil, RefusedPrivate
	}
	file, err = root.OpenFile(entry, flags|auditFileFlags, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err = verifyAuditFile(file)
	if err == nil && !os.SameFile(before, info) {
		err = RefusedOwner
	}
	if err != nil {
		return nil, nil, errors.Join(err, file.Close())
	}
	return file, info, nil
}

func createAuditFile(root *os.Root, entry string) (*os.File, error) {
	file, err := root.OpenFile(entry, os.O_CREATE|os.O_EXCL|os.O_APPEND|os.O_WRONLY|auditFileFlags, 0600)
	if err != nil {
		return nil, err
	}
	if _, err = verifyAuditFile(file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func verifyAuditFile(file *os.File) (os.FileInfo, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !private(info, false) {
		return nil, RefusedPrivate
	}
	if !trustedFilesystem(file) {
		return nil, RefusedFilesystem
	}
	return info, nil
}
