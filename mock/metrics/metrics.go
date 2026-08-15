package mockmetrics

import "time"

type TimingCall struct {
	Stat     string
	Duration time.Duration
}

type CountCall struct {
	Stat  string
	Value int64
}

type GaugeCall struct {
	Stat  string
	Value float64
}

// Client is a lightweight test double for the StatsD client contract.
type Client struct {
	TimingFunc    func(stat string, d time.Duration)
	IncrementFunc func(stat string)
	CountFunc     func(stat string, value int64)
	GaugeFunc     func(stat string, value float64)
	CloseFunc     func() error

	Timings    []TimingCall
	Increments []string
	Counts     []CountCall
	Gauges     []GaugeCall

	CloseErr error
}

func (c *Client) Timing(stat string, d time.Duration) {
	c.Timings = append(c.Timings, TimingCall{Stat: stat, Duration: d})
	if c.TimingFunc != nil {
		c.TimingFunc(stat, d)
	}
}

func (c *Client) Increment(stat string) {
	c.Increments = append(c.Increments, stat)
	if c.IncrementFunc != nil {
		c.IncrementFunc(stat)
	}
}

func (c *Client) Count(stat string, value int64) {
	c.Counts = append(c.Counts, CountCall{Stat: stat, Value: value})
	if c.CountFunc != nil {
		c.CountFunc(stat, value)
	}
}

func (c *Client) Gauge(stat string, value float64) {
	c.Gauges = append(c.Gauges, GaugeCall{Stat: stat, Value: value})
	if c.GaugeFunc != nil {
		c.GaugeFunc(stat, value)
	}
}

func (c *Client) Close() error {
	if c.CloseFunc != nil {
		return c.CloseFunc()
	}
	return c.CloseErr
}
