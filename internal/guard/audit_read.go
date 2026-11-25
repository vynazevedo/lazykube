package guard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	AuditPageSize   = 200
	auditReadBudget = 2 << 20
	auditLineLimit  = 16 << 10
	auditSegments   = 10
	auditSegmentMax = 10 << 20
)

var ErrAuditUnreadable = errors.New("audit local ilegível")

type AuditCursor struct {
	started bool
	segment int
	end     int64
	info    os.FileInfo
}

func (c AuditCursor) Started() bool {
	return c.started
}

type AuditPage struct {
	Events  []AuditEvent
	Next    AuditCursor
	More    bool
	Pending bool
	Rotated bool
	Invalid int
	Missing bool
}

func auditSegmentName(name string, i int) string {
	if i == 0 {
		return name
	}
	return fmt.Sprintf("%s.%d", name, i)
}

func ReadAudit(ctx context.Context, path string, cursor AuditCursor, limit int) (page AuditPage, readErr error) {
	if limit <= 0 || limit > AuditPageSize {
		limit = AuditPageSize
	}
	if !filepath.IsAbs(path) {
		return page, ErrAuditUnreadable
	}
	dir, name := filepath.Dir(path), filepath.Base(path)
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		page.Missing = true
		return page, nil
	}
	if err != nil || !private(info, true) {
		return page, ErrAuditUnreadable
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return page, ErrAuditUnreadable
	}
	defer func() { readErr = errors.Join(readErr, root.Close()) }()
	if current, statErr := root.Stat("."); statErr != nil || !os.SameFile(info, current) {
		return page, ErrAuditUnreadable
	}
	r := auditReader{ctx: ctx, root: root, name: name, limit: limit, budget: auditReadBudget, page: &page}
	start := 0
	if cursor.started {
		start = cursor.segment
	}
	for seg := start; seg < auditSegments; seg++ {
		if ctx.Err() != nil {
			return AuditPage{}, ErrAuditUnreadable
		}
		resume := cursor.started && seg == cursor.segment
		done, err := r.segment(seg, resume, cursor)
		if err != nil {
			return AuditPage{}, err
		}
		if page.Rotated {
			return AuditPage{Rotated: true}, nil
		}
		if done {
			page.More = true
			return page, nil
		}
	}
	page.Next = AuditCursor{started: true, segment: auditSegments}
	return page, nil
}

type auditReader struct {
	ctx    context.Context
	root   *os.Root
	name   string
	limit  int
	budget int64
	page   *AuditPage
}

func (r *auditReader) segment(seg int, resume bool, cursor AuditCursor) (done bool, segErr error) {
	entry := auditSegmentName(r.name, seg)
	linfo, err := r.root.Lstat(entry)
	if os.IsNotExist(err) {
		if resume {
			r.page.Rotated = true
		}
		return false, nil
	}
	if err != nil || !private(linfo, false) || linfo.Size() > auditSegmentMax {
		return false, ErrAuditUnreadable
	}
	file, err := r.root.OpenFile(entry, os.O_RDONLY|auditReadFlags, 0)
	if err != nil {
		return false, ErrAuditUnreadable
	}
	defer func() { segErr = errors.Join(segErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil || !private(info, false) || !os.SameFile(linfo, info) {
		return false, ErrAuditUnreadable
	}
	end := info.Size()
	if resume {
		if cursor.info == nil || !os.SameFile(cursor.info, info) || cursor.end > end {
			r.page.Rotated = true
			return false, nil
		}
		end = cursor.end
	}
	trimTail := !resume
	for end > 0 {
		if r.ctx.Err() != nil {
			return false, ErrAuditUnreadable
		}
		if len(r.page.Events) >= r.limit || r.budget <= auditLineLimit {
			r.page.Next = AuditCursor{started: true, segment: seg, end: end, info: info}
			return true, nil
		}
		window := min(end, r.budget)
		start := end - window
		buf := make([]byte, window)
		if n, readErr := file.ReadAt(buf, start); int64(n) != window {
			return false, errors.Join(ErrAuditUnreadable, readErr)
		}
		r.budget -= window
		if trimTail {
			trimTail = false
			if buf[len(buf)-1] != '\n' {
				if seg == 0 {
					r.page.Pending = true
				} else {
					r.page.Invalid++
				}
				cut := bytes.LastIndexByte(buf, '\n')
				if cut < 0 && start > 0 {
					return false, ErrAuditUnreadable
				}
				end = start + int64(cut+1)
				buf = buf[:cut+1]
			}
		}
		boundary := 0
		if start > 0 {
			first := bytes.IndexByte(buf, '\n')
			if first < 0 || first == len(buf)-1 {
				return false, ErrAuditUnreadable
			}
			boundary = first + 1
		}
		lines := buf[boundary:]
		pos := end
		for len(lines) > 0 {
			body := lines[:len(lines)-1]
			cut := bytes.LastIndexByte(body, '\n')
			line := body[cut+1:]
			lines = body[:cut+1]
			if len(r.page.Events) >= r.limit {
				r.page.Next = AuditCursor{started: true, segment: seg, end: pos, info: info}
				return true, nil
			}
			pos -= int64(len(line) + 1)
			if event, ok := parseAuditLine(line); ok {
				r.page.Events = append(r.page.Events, event)
			} else {
				r.page.Invalid++
			}
		}
		end = start + int64(boundary)
	}
	return false, nil
}

func parseAuditLine(line []byte) (AuditEvent, bool) {
	var e AuditEvent
	if len(line) == 0 || len(line) > auditLineLimit {
		return e, false
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil || dec.More() || e.SchemaVersion != 1 || e.Timestamp.IsZero() {
		return AuditEvent{}, false
	}
	for _, s := range []*string{&e.OperationID, &e.Phase, &e.Context, &e.Identity, &e.Namespace, &e.Group, &e.Resource, &e.Name, &e.UID} {
		*s = auditText(*s)
	}
	e.Outcome = Reason(auditText(string(e.Outcome)))
	return e, true
}
