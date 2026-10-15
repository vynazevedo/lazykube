package gui

import "sync"

type renderedContent struct {
	mu    sync.Mutex
	value string
	valid bool
}

func (c *renderedContent) set(value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = value
	c.valid = true
}

func (c *renderedContent) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = ""
	c.valid = false
}

func (c *renderedContent) matches(value string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.valid && c.value == value
}
