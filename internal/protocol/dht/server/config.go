package server

import "time"

const defaultMaxConcurrentQueries int64 = 512

type Config struct {
	Port                 uint16
	QueryTimeout         time.Duration
	MaxConcurrentQueries int64
}

func NewDefaultConfig() Config {
	return Config{
		Port:                 3334,
		QueryTimeout:         time.Second * 4,
		MaxConcurrentQueries: defaultMaxConcurrentQueries,
	}
}

func (c Config) queryConcurrencyLimit() int64 {
	if c.MaxConcurrentQueries <= 0 {
		return defaultMaxConcurrentQueries
	}

	return c.MaxConcurrentQueries
}
