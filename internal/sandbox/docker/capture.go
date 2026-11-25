package docker

import "fmt"

// capture is a writer that keeps the first and last limit/2 bytes written to
// it, so huge output can't exhaust host memory.
type capture struct {
	limit int
	head  []byte
	tail  []byte // up to limit bytes; the last limit/2 count
	total int
}

// Write never fails.
func (c *capture) Write(p []byte) (int, error) {
	n, half := len(p), c.limit/2
	c.total += n
	room := min(half-len(c.head), len(p))
	c.head = append(c.head, p[:room]...)
	p = p[room:]
	p = p[max(0, len(p)-half):]
	if len(c.tail)+len(p) > c.limit {
		c.tail = append(c.tail[:0], c.tail[len(c.tail)-half:]...)
	}
	c.tail = append(c.tail, p...)
	return n, nil
}

func (c *capture) String() string {
	tail := c.tail[max(0, len(c.tail)-c.limit/2):]
	if dropped := c.total - len(c.head) - len(tail); dropped > 0 {
		return fmt.Sprintf("%s\n[discarded %d bytes]\n%s", c.head, dropped, tail)
	}
	return string(c.head) + string(tail)
}
