package contract

import "time"

type StatsdClient interface {
	Timing(stat string, d time.Duration)
	Increment(stat string)
	Count(stat string, value int64)
	Gauge(stat string, value float64)
	Close() error
}
