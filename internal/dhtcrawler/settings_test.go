package dhtcrawler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCrawlerSettingsUseConfiguredReseedInterval(t *testing.T) {
	t.Parallel()

	config := NewDefaultConfig()
	config.ReseedBootstrapNodesInterval = 37 * time.Second

	settings := crawlerSettingsFromConfig(config)

	require.Equal(t, config.ReseedBootstrapNodesInterval, settings.reseedBootstrapNodesInterval)
}

func TestCrawlerSettingsUseDefaultForNonPositiveReseedInterval(t *testing.T) {
	t.Parallel()

	settings := crawlerSettingsFromConfig(Config{})

	require.Equal(t, NewDefaultConfig().ReseedBootstrapNodesInterval, settings.reseedBootstrapNodesInterval)
}
