package gui

type VerticalPosition struct {
	Cursor int
	Origin int
}

func NextVerticalPosition(current VerticalPosition, dy, height, totalLines int) VerticalPosition {
	next := current
	cy := current.Cursor + dy

	if cy < 0 {
		if next.Origin > 0 {
			next.Origin--
		}
		return next
	}

	if current.Origin+cy >= totalLines {
		return next
	}

	if cy >= height {
		next.Origin++
		return next
	}

	next.Cursor = cy
	return next
}
