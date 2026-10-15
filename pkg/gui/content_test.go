package gui

import "testing"

func TestRenderedContent(t *testing.T) {
	var c renderedContent
	if c.matches("") {
		t.Fatal("zero value must not match")
	}
	c.set("options")
	if !c.matches("options") {
		t.Fatal("expected match after set")
	}
	if c.matches("other") {
		t.Fatal("different content must not match")
	}
	c.reset()
	if c.matches("options") {
		t.Fatal("reset must invalidate content")
	}
}
