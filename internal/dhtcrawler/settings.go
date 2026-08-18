package dhtcrawler

import "time"

type crawlerSettings struct {
	reseedBootstrapNodesInterval time.Duration
}

func crawlerSettingsFromConfig(config Config) crawlerSettings {
	// Adapted from o51r15/bitmagnet@9711ecbbb6c7d9644b99f78b92e4ade986dad24d:
	// use the configured interval rather than a factory-level hard-coded value.
	interval := config.ReseedBootstrapNodesInterval
	if interval <= 0 {
		interval = NewDefaultConfig().ReseedBootstrapNodesInterval
	}

	return crawlerSettings{
		reseedBootstrapNodesInterval: interval,
	}
}
