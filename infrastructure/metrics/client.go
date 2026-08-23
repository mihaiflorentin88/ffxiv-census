package metrics

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type client struct {
	conn   net.Conn
	prefix string
}

// New initialises a UDP StatsD writer using the provided endpoint (host:port) and prefix.
func New(endpoint, prefix string) (contract.StatsdClient, error) {
	conn, err := net.Dial("udp", endpoint)
	if err != nil {
		return nil, fmt.Errorf("connect to statsd endpoint %s: %w", endpoint, err)
	}
	return &client{conn: conn, prefix: strings.Trim(prefix, ".")}, nil
}

func (c *client) Timing(stat string, d time.Duration) {
	ms := d.Milliseconds()
	c.send(fmt.Sprintf("%s:%d|ms", c.compose(stat), ms))
}

func (c *client) Increment(stat string) {
	c.send(fmt.Sprintf("%s:1|c", c.compose(stat)))
}

func (c *client) Count(stat string, value int64) {
	c.send(fmt.Sprintf("%s:%d|c", c.compose(stat), value))
}

func (c *client) Gauge(stat string, value float64) {
	c.send(fmt.Sprintf("%s:%f|g", c.compose(stat), value))
}

func (c *client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *client) compose(stat string) string {
	segment := strings.Trim(stat, ".")
	if c.prefix == "" {
		return segment
	}
	if segment == "" {
		return c.prefix
	}
	return c.prefix + "." + segment
}

func (c *client) send(metric string) {
	if c.conn == nil {
		return
	}
	if _, err := c.conn.Write([]byte(metric)); err != nil {
		logging.Error("Statsd send failed", fmt.Sprintf("%v", err))
	}
}
